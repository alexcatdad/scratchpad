import { z } from "zod";
import { AiService } from "./ai";
import { Auth, type Identity } from "./auth";
import { assertRelationshipSafe, projectContext } from "./context";
import {
  type Actor,
  ApiError,
  canonical,
  captureSchema,
  defaults,
  type Entity,
  id,
  type JsonObject,
  normalizeRemote,
  now,
  type recordTypes,
  relationshipTypes,
  renderPayload,
  requireValue,
  settingsSchema,
} from "./domain";
import { GithubAccess, type GithubOAuth } from "./github-access";
import {
  fetchGithubProfile,
  githubUsername,
  presentGithubProfile,
} from "./github-profile";
import { exportKinds, importLegacy, importNative } from "./imports";
import { requestClient } from "./request-client";
import { Store } from "./store";
import { compareTimestamps } from "./timestamps";

const object = z.record(z.string(), z.unknown());
const nonempty = z.string().trim().min(1).max(500);
const bodyLimit = 8 * 1024 * 1024;
function response(
  value: unknown,
  status = 200,
  headers: Record<string, string> = {},
): Response {
  return Response.json(value, {
    status,
    headers: {
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
      ...headers,
    },
  });
}
function expected(body: JsonObject, request: Request): number {
  const value =
    body.expectedVersion ??
    request.headers.get("if-match")?.replaceAll('"', "");
  requireValue(
    value !== undefined,
    "PRECONDITION_REQUIRED",
    "Supply expectedVersion or If-Match.",
    428,
  );
  return z.coerce.number().int().positive().parse(value);
}
function publicError(error: unknown): Response {
  if (error instanceof ApiError)
    return response(
      {
        error: {
          code: error.code,
          message: error.message,
          details: error.details,
        },
      },
      error.status,
    );
  if (error instanceof z.ZodError)
    return response(
      {
        error: {
          code: "VALIDATION_FAILED",
          message: "Request validation failed.",
          details: error.issues,
        },
      },
      400,
    );
  if (error instanceof SyntaxError)
    return response(
      {
        error: { code: "VALIDATION_FAILED", message: "Invalid JSON request." },
      },
      400,
    );
  console.error(
    "Scratchpad request failed:",
    error instanceof Error ? error.name : "unknown error",
  );
  return response(
    {
      error: {
        code: "INTERNAL_ERROR",
        message: "The request could not be completed.",
      },
    },
    500,
  );
}
export function createApi(config: {
  databasePath: string;
  databaseUrl?: string;
  origin: string;
  githubFetch?: typeof fetch;
  githubOAuth?: GithubOAuth;
  githubAuthFetch?: typeof fetch;
  clock?: () => number;
  trustedProxies?: string[];
  pgvector?: boolean;
}) {
  const store = new Store(config.databasePath, config.databaseUrl, {
      pgvector: config.pgvector,
    }),
    auth = new Auth(store, config.origin, config.clock),
    ai = new AiService(store, undefined, undefined, config.origin),
    github = new GithubAccess(
      store,
      auth,
      config.githubOAuth,
      config.githubAuthFetch,
    );
  auth.github = github;
  async function entity(kind: string, key: string): Promise<Entity> {
    const value = await store.get(kind, key);
    requireValue(
      value,
      kind === "project"
        ? "PROJECT_NOT_FOUND"
        : kind === "record"
          ? "RECORD_NOT_FOUND"
          : "NOT_FOUND",
      `${kind} not found.`,
      404,
    );
    return kind === "project"
      ? { ...value, settings: settingsSchema.parse(value.settings) }
      : value;
  }
  async function projectCreate(
    name: string,
    kind = "normal",
    identity?: string,
    sourceKind = "git_remote",
  ): Promise<Entity> {
    const project = await store.insert("project", {
      id: id("proj"),
      name,
      slug: name.toLowerCase().replace(/[^a-z0-9]+/g, "-"),
      kind,
      settings:
        kind === "external"
          ? defaults(kind)
          : settingsSchema.parse(
              (await store.get("settings", "global"))?.defaultProjectSettings ??
                defaults(kind),
            ),
      updatedAt: now(),
    });
    if (identity) {
      await store.addIdentity(identity, project.id);
      await store.insert("source", {
        id: id("source"),
        projectId: project.id,
        kind: sourceKind,
        identity,
      });
    }
    return project;
  }
  async function createRecord(
    projectId: string,
    submitted: unknown,
    gitContext: unknown,
    actor: Actor,
  ): Promise<JsonObject> {
    const project = await entity("project", projectId),
      record = captureSchema.parse(submitted);
    requireValue(
      settingsSchema
        .parse(project.settings)
        .enabledRecordTypes.includes(record.type),
      "RECORD_TYPE_DISABLED",
      "This record type is disabled for the project.",
      403,
    );
    const content = renderPayload(record.type, record.payload);
    const saved = await store.insert("record", {
      ...record,
      id: id("rec"),
      projectId,
      content,
      payloadVersion: 1,
      recordedAt: now(),
      actor: { ...record.actor, ...actor },
      gitContext:
        gitContext === undefined ? undefined : object.parse(gitContext),
    });
    await store.insert("metadata", {
      id: saved.id,
      recordId: saved.id,
      tags: [],
      archived: false,
      updatedAt: now(),
    });
    await store.indexRecord(saved.id, record.title, content);
    await store.audit(
      "record.created",
      "record",
      saved.id,
      actor,
      undefined,
      saved,
    );
    const settings = settingsSchema.parse(project.settings);
    return {
      record: saved,
      mirror: {
        eligible:
          settings.repoMirroring.enabled &&
          settings.repoMirroring.recordTypes.includes(record.type),
      },
    };
  }
  async function recordDetail(key: string): Promise<JsonObject> {
    const relatedIds = new Set([
      key,
      ...(await store.list("relationship"))
        .filter((link) => link.fromRecordId === key || link.toRecordId === key)
        .map((link) => link.id),
      ...(await store.list("evidence"))
        .filter((item) => item.recordId === key)
        .map((item) => item.id),
    ]);
    return {
      record: await entity("record", key),
      metadata: await store.get("metadata", key),
      revisions: (await store.list("revision")).filter(
        (r) => r.recordId === key,
      ),
      relationships: (await store.list("relationship")).filter(
        (r) => r.fromRecordId === key || r.toRecordId === key,
      ),
      evidence: (await store.list("evidence")).filter(
        (r) => r.recordId === key,
      ),
      mirror: await store.get("mirror", key),
      audit: (await store.list("audit")).filter((r) =>
        relatedIds.has(String(r.entityId)),
      ),
    };
  }
  async function search(params: URLSearchParams): Promise<JsonObject> {
    const limit = z.coerce
      .number()
      .int()
      .min(1)
      .max(100)
      .parse(params.get("limit") ?? 30);
    let records = await store.list("record");
    const metadataById = new Map(
      (await store.list("metadata")).map((m) => [m.id, m]),
    );
    const allRelationships = await store.list("relationship");
    const q = params.get("q");
    if (q) {
      const terms = q.match(/[\p{L}\p{N}_]+/gu)?.slice(0, 30) ?? [];
      if (!terms.length) records = [];
      else {
        const ids = new Set(await store.searchRecords(terms));
        records = records.filter((r) => ids.has(r.id));
      }
    }
    for (const field of [
      "projectId",
      "type",
      "authority",
      "confidence",
    ] as const) {
      const value = params.get(field);
      if (value) records = records.filter((r) => r[field] === value);
    }
    const projectIds = params.getAll("projectIds"),
      types = params.getAll("types");
    if (projectIds.length)
      records = records.filter((r) => projectIds.includes(String(r.projectId)));
    if (types.length)
      records = records.filter((r) => types.includes(String(r.type)));
    const tag = params.get("tag"),
      branch = params.get("branch"),
      from = params.get("from"),
      to = params.get("to");
    if (tag)
      records = records.filter((r) =>
        (metadataById.get(r.id)?.tags as string[] | undefined)?.includes(tag),
      );
    if (branch)
      records = records.filter(
        (r) => (r.gitContext as JsonObject | undefined)?.branch === branch,
      );
    if (from) {
      z.iso.datetime().parse(from);
      records = records.filter(
        (r) =>
          compareTimestamps(String(r.happenedAt ?? r.recordedAt), from) >= 0,
      );
    }
    if (to) {
      z.iso.datetime().parse(to);
      records = records.filter(
        (r) => compareTimestamps(String(r.happenedAt ?? r.recordedAt), to) <= 0,
      );
    }
    const status = params.get("status"),
      relationshipType = params.get("relationship"),
      relatedTo = params.get("relatedTo"),
      source = params.get("source"),
      gitPath = params.get("gitPath");
    if (status)
      records = records.filter((record) => {
        const metadata = metadataById.get(record.id);
        const replacements = allRelationships.filter(
          (link) => link.toRecordId === record.id && link.status === "accepted",
        );
        const lifecycle = metadata?.archived
          ? "archived"
          : replacements.some((link) => link.type === "replaces")
            ? "superseded"
            : replacements.some((link) => link.type === "partially_replaces")
              ? "partially_superseded"
              : (metadata?.legacyStatus ?? "active");
        return lifecycle === status;
      });
    if (relationshipType || relatedTo) {
      const links = (await store.list("relationship")).filter(
        (link) =>
          link.status !== "rejected" &&
          (!relationshipType || link.type === relationshipType) &&
          (!relatedTo ||
            link.fromRecordId === relatedTo ||
            link.toRecordId === relatedTo),
      );
      records = records.filter((record) =>
        links.some(
          (link) =>
            link.fromRecordId === record.id || link.toRecordId === record.id,
        ),
      );
    }
    if (source)
      records = records.filter((record) => {
        const provenance = record.provenance as JsonObject | undefined;
        const imported = provenance?.import as JsonObject | undefined;
        const actor = record.actor as JsonObject | undefined;
        return (
          imported?.sourceName === source ||
          actor?.client === source ||
          actor?.displayName === source
        );
      });
    if (gitPath)
      records = records.filter(
        (record) =>
          (record.gitContext as JsonObject | undefined)?.rootPathHint ===
          gitPath,
      );
    const archived = params.get("archived");
    if (archived)
      records = records.filter(
        (r) =>
          Boolean(metadataById.get(r.id)?.archived) === (archived === "true"),
      );
    records.sort(
      (a, b) =>
        b.createdAt.localeCompare(a.createdAt) || b.id.localeCompare(a.id),
    );
    const cursor = params.get("cursor");
    if (cursor) {
      let decoded: unknown;
      try {
        decoded = JSON.parse(Buffer.from(cursor, "base64url").toString());
      } catch {
        throw new ApiError(400, "VALIDATION_FAILED", "Invalid cursor.");
      }
      const [date, key] = z.tuple([z.string(), z.string()]).parse(decoded);
      records = records.filter(
        (r) => r.createdAt < date || (r.createdAt === date && r.id < key),
      );
    }
    const page = records.slice(0, limit),
      last = page.at(-1);
    return {
      records: page.map((r) => ({
        ...r,
        metadata: metadataById.get(r.id),
      })),
      nextCursor:
        records.length > limit && last
          ? Buffer.from(JSON.stringify([last.createdAt, last.id])).toString(
              "base64url",
            )
          : null,
    };
  }
  async function handleRequest(
    request: Request,
    clientAddress?: string,
  ): Promise<Response> {
    const client = requestClient(request, clientAddress, config.trustedProxies);
    try {
      const url = new URL(request.url),
        path = url.pathname.replace(/\/$/, "") || "/",
        method = request.method;
      if (method === "GET" && path === "/health")
        return response({ status: "ok" });
      if (method === "GET" && path === "/ready") {
        await store.assertReady();
        return response({ status: "ready", database: store.backend });
      }
      requireValue(
        path.startsWith("/api/v1/"),
        "NOT_FOUND",
        "Endpoint not found.",
        404,
      );
      // Enforce limits while streaming, including requests with no Content-Length.
      let body: JsonObject = {};
      if (!["GET", "HEAD"].includes(method) && request.body) {
        const maxBodyBytes =
          path === "/api/v1/import" ? 64 * 1024 * 1024 : bodyLimit;
        const reader = request.body.getReader();
        const parts: Uint8Array[] = [];
        let size = 0;
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          size += value.byteLength;
          if (size > maxBodyBytes) {
            await reader.cancel();
            throw new ApiError(
              413,
              "VALIDATION_FAILED",
              `Request exceeds ${maxBodyBytes / (1024 * 1024)} MiB.`,
            );
          }
          parts.push(value);
        }
        const raw = Buffer.concat(parts).toString("utf8");
        if (raw) body = object.parse(JSON.parse(raw));
      }
      const route = path.slice("/api/v1".length);
      if (route === "/auth/status" && method === "GET") {
        let authenticated = false;
        try {
          await auth.identify(request);
          authenticated = true;
        } catch {
          /* Status intentionally supports signed-out clients. */
        }
        return response({
          githubConfigured: Boolean(config.githubOAuth),
          initialized: await auth.initialized(),
          authenticated,
        });
      }
      if (route.startsWith("/auth/") && method === "POST") {
        const publicRoutes = [
          "/auth/github/options",
          "/auth/mcp/challenge",
          "/auth/mcp/verify",
          "/auth/register/options",
          "/auth/register/verify",
          "/auth/login/options",
          "/auth/login/verify",
        ];
        const managementRoutes = [
          "/auth/github/sync",
          "/auth/github/block",
          "/auth/logout",
          "/auth/credentials/challenge",
          "/auth/credentials/verify",
        ];
        requireValue(
          publicRoutes.includes(route) || managementRoutes.includes(route),
          "NOT_FOUND",
          "Endpoint not found.",
          404,
        );
        if (publicRoutes.includes(route) && !route.startsWith("/auth/mcp/"))
          auth.checkOrigin(request);
        if (
          managementRoutes.includes(route) &&
          !request.headers
            .get("authorization")
            ?.match(/^Bearer ([A-Za-z0-9_-]+)$/)
        )
          auth.checkOrigin(request);
        // Proof lookup has its own bounded client lane before any hashing or SQL.
        // Anonymous login traffic cannot spend this protected lookup allowance.
        const resolvesProof =
          managementRoutes.includes(route) ||
          route.startsWith("/auth/register/") ||
          (route === "/auth/github/options" && body.intent !== "login");
        if (resolvesProof) auth.throttle(`proof-client:${client}`);
        let allowance = `client:${client}`;
        try {
          if (
            managementRoutes.includes(route) ||
            (route.startsWith("/auth/register/") && !body.setupToken)
          ) {
            const identity = await auth.identify(request);
            allowance = `credential:${identity.credential.id}`;
          } else if (route.startsWith("/auth/register/") && body.setupToken) {
            const token = await auth.setup(body.setupToken);
            allowance = `setup:${token.id}`;
          }
        } catch (error) {
          auth.throttle(allowance);
          throw error;
        }
        if (route !== "/auth/github/options") auth.throttle(allowance);
      }
      if (route === "/auth/github/options" && method === "POST") {
        let admitted = false;
        let result: Awaited<ReturnType<typeof github.options>>;
        try {
          result = await github.options(body, request, (allowance) => {
            auth.throttle(allowance ?? `client:${client}`);
            admitted = true;
          });
        } catch (error) {
          if (
            !admitted &&
            !(error instanceof ApiError && error.code === "RATE_LIMITED")
          )
            auth.throttle(`client:${client}`);
          throw error;
        }
        return response({ authorizationUrl: result.authorizationUrl }, 200, {
          "Set-Cookie": result.cookie,
        });
      }
      if (route === "/auth/github/callback" && method === "GET") {
        let release: (() => void) | undefined;
        try {
          const result = await github.callback(request, (allowance) => {
            auth.throttle(allowance);
            release = auth.reserveVerification(
              allowance.startsWith("oauth:") ? "oauth" : "proof",
            );
          });
          const headers = new Headers({
            Location: "/",
            "Cache-Control": "no-store",
          });
          headers.append("Set-Cookie", auth.cookie(result.accessToken));
          headers.append("Set-Cookie", result.clearState);
          return new Response(null, { status: 302, headers });
        } catch (error) {
          if (
            !release &&
            !(error instanceof ApiError && error.code === "RATE_LIMITED")
          )
            auth.throttle(`client:${client}`);
          if (error instanceof ApiError && error.code === "RATE_LIMITED")
            return response(
              { error: { code: error.code, message: error.message } },
              429,
              { "Cache-Control": "no-store" },
            );
          const allowed = [
            "AUTH_INVALID",
            "AUTH_FRESH_REQUIRED",
            "CONFLICT",
            "GITHUB_NOT_CONFIGURED",
            "GITHUB_UNAVAILABLE",
            "GITHUB_RATE_LIMITED",
          ];
          const code =
            error instanceof ApiError && allowed.includes(error.code)
              ? error.code
              : "AUTH_INVALID";
          return new Response(null, {
            status: 303,
            headers: {
              Location: `/?authError=${code}`,
              "Cache-Control": "no-store",
              "Set-Cookie": github.clearStateCookie(),
            },
          });
        } finally {
          release?.();
        }
      }
      if (route.startsWith("/auth/")) {
        if (method === "POST" && route === "/auth/mcp/challenge")
          return response(await auth.sshChallenge(body));
        if (method === "POST" && route === "/auth/mcp/verify")
          return response(await auth.verify(() => auth.sshVerify(body)));
        if (
          method === "POST" &&
          [
            "/auth/register/options",
            "/auth/register/verify",
            "/auth/login/options",
            "/auth/login/verify",
          ].includes(route)
        ) {
          auth.checkOrigin(request);
          let identity: Identity | undefined;
          if (!body.setupToken && route.startsWith("/auth/register/"))
            identity = await auth.identify(request);
          if (route === "/auth/register/options")
            return response(await auth.registrationOptions(body, identity));
          if (route === "/auth/login/options")
            return response(await auth.loginOptions());
          const session =
            route === "/auth/register/verify"
              ? await auth.verify(() => auth.registrationVerify(body, identity))
              : await auth.verify(() => auth.loginVerify(body));
          return response(
            { authenticated: true, expiresAt: session.expiresAt },
            200,
            { "Set-Cookie": auth.cookie(session.accessToken) },
          );
        }
      }
      const identity = await auth.identify(request),
        actor = identity.actor;
      if (route === "/auth/github" && method === "GET")
        return response(await github.status(identity));
      if (route === "/auth/github" && method === "DELETE") {
        await github.unlink(identity);
        return response({ unlinked: true });
      }
      if (route === "/auth/github/sync" && method === "POST") {
        await github.status(identity);
        await github.sync();
        return response(await github.status(identity));
      }
      if (route === "/auth/github/block" && method === "POST") {
        await github.block(body, identity);
        return response(await github.status(identity));
      }

      if (route === "/profile" && method === "GET") {
        const profile = (await store.get("profile", "owner-profile")) ?? {
          id: "owner-profile",
          displayName: String(
            (await store.get("owner", "owner"))?.displayName ?? "Owner",
          ),
          version: 0,
          github: null,
        };
        return response({
          profile: { ...profile, github: presentGithubProfile(profile.github) },
          database: {
            engine: store.backend === "postgres" ? "postgresql" : "sqlite",
          },
        });
      }
      if (route === "/profile" && method === "PATCH")
        return response(
          await store.atomic(async () => {
            const displayName = z
              .string()
              .trim()
              .min(1)
              .max(200)
              .parse(body.displayName);
            const version = z
              .number()
              .int()
              .nonnegative()
              .parse(body.expectedVersion);
            const previous = await store.get("profile", "owner-profile");
            requireValue(
              (previous?.version ?? 0) === version,
              "CONFLICT",
              "Profile changed. Read it again.",
              409,
            );
            const profile = previous
              ? await store.update(
                  "profile",
                  { ...previous, displayName },
                  version,
                )
              : await store.insert("profile", {
                  id: "owner-profile",
                  displayName,
                });
            await store.audit(
              "profile.updated",
              "profile",
              profile.id,
              actor,
              previous,
              profile,
            );
            return {
              profile: {
                ...profile,
                github: presentGithubProfile(profile.github),
              },
            };
          }),
        );
      if (
        route === "/profile/github" &&
        (method === "POST" || method === "DELETE")
      ) {
        const version = z
          .number()
          .int()
          .nonnegative()
          .parse(body.expectedVersion);
        const before = await store.get("profile", "owner-profile");
        requireValue(
          (before?.version ?? 0) === version,
          "CONFLICT",
          "Profile changed. Read it again.",
          409,
        );
        // Network access stays outside the transaction. Recheck the version before saving.
        const github =
          method === "POST"
            ? await fetchGithubProfile(
                githubUsername.parse(body.username),
                config.githubFetch,
              )
            : null;
        return response(
          await store.atomic(async () => {
            const previous = await store.get("profile", "owner-profile");
            requireValue(
              (previous?.version ?? 0) === version,
              "CONFLICT",
              "Profile changed. Read it again.",
              409,
            );
            const profile = previous
              ? await store.update("profile", { ...previous, github }, version)
              : await store.insert("profile", {
                  id: "owner-profile",
                  displayName: String(
                    (await store.get("owner", "owner"))?.displayName ?? "Owner",
                  ),
                  github,
                });
            await store.audit(
              method === "DELETE"
                ? "profile.github.unlinked"
                : "profile.github.updated",
              "profile",
              profile.id,
              actor,
              previous,
              profile,
            );
            return {
              profile: {
                ...profile,
                github: presentGithubProfile(profile.github),
              },
            };
          }),
        );
      }
      if (route === "/ai/settings" && method === "GET")
        return response(await ai.settings());
      if (route === "/ai/settings" && method === "PATCH")
        return response(
          await ai.configure(
            {
              ...body,
              expectedVersion: z.coerce
                .number()
                .int()
                .nonnegative()
                .parse(
                  body.expectedVersion ??
                    request.headers.get("if-match")?.replaceAll('"', ""),
                ),
            },
            actor,
          ),
        );
      if (route === "/ai/test" && method === "POST")
        return response(await ai.testProvider());
      if (route === "/ai/jobs" && method === "GET")
        return response({ jobs: await ai.jobs() });
      if (route === "/ai/jobs" && method === "POST")
        return response({ job: await ai.enqueue(body, actor) }, 202);
      if (/^\/ai\/jobs\/[^/]+\/retry$/.test(route) && method === "POST")
        return response(
          {
            job: await ai.retry(
              route.split("/")[3] ?? "",
              actor,
              expected(body, request),
            ),
          },
          202,
        );
      if (route === "/suggestions" && method === "GET")
        return response({
          suggestions: await ai.artifacts({
            projectId: url.searchParams.get("projectId") ?? undefined,
            projectIds: url.searchParams.getAll("projectIds").length
              ? url.searchParams.getAll("projectIds")
              : undefined,
            crossProject: url.searchParams.get("crossProject") === "true",
          }),
        });
      if (
        /^\/suggestions\/[^/]+\/(accept|reject)$/.test(route) &&
        method === "POST"
      )
        return response({
          suggestion: await ai.review(
            route.split("/")[2] ?? "",
            route.endsWith("/accept") ? "accepted" : "rejected",
            actor,
            expected(body, request),
          ),
        });
      if (route === "/search/semantic" && method === "POST")
        return response(await ai.semanticSearch(body));
      if (route === "/summaries/export" && method === "POST")
        return response(
          { job: await ai.enqueue({ ...body, type: "export" }, actor) },
          202,
        );
      if (route === "/auth/logout" && method === "POST") {
        await auth.logout(identity);
        return response({ authenticated: false }, 200, {
          "Set-Cookie": auth.clearCookie(),
        });
      }
      if (route === "/auth/credentials" && method === "GET")
        return response({ credentials: await auth.publicCredentials() });
      if (route.startsWith("/auth/credentials")) {
        requireValue(
          identity.browser,
          "AUTH_INVALID",
          "Manage credentials from an authenticated browser.",
          403,
        );
        if (method === "POST" && route === "/auth/credentials/challenge")
          return response(await auth.sshChallenge(body, identity));
        if (method === "POST" && route === "/auth/credentials/verify")
          return response(
            await auth.verify(() => auth.sshVerify(body, identity)),
          );
        if (method === "DELETE") {
          await auth.revoke(route.split("/")[3] ?? "", identity);
          return response({ revoked: true });
        }
      }
      if (route === "/projects" && method === "GET")
        return response({
          projects: await Promise.all(
            (await store.list("project")).map(async (p) => ({
              ...p,
              settings: settingsSchema.parse(p.settings),
              sources: (await store.list("source")).filter(
                (s) => s.projectId === p.id,
              ),
            })),
          ),
        });
      if (route === "/projects/resolve" && method === "POST") {
        const context = object.parse(body.context),
          git = object.parse(context.git ?? {});
        requireValue(
          typeof git.remote === "string",
          "PROJECT_IDENTITY_REQUIRED",
          "A Git remote or explicit project selection is required.",
        );
        const remote = normalizeRemote(git.remote);
        const project = await store.atomic(async () => {
          const row = await store.resolveIdentity(remote);
          if (row) return await entity("project", row);
          const created = await projectCreate(
            remote.split("/").at(-1) ?? remote,
            "normal",
            remote,
          );
          await store.audit(
            "project.created",
            "project",
            created.id,
            actor,
            undefined,
            created,
          );
          return created;
        });
        return response({
          project,
          resolution: { source: "git_remote", confidence: "high" },
        });
      }
      if (route === "/projects/resolve-explicit" && method === "POST") {
        if (body.projectId)
          return response({
            project: await entity("project", nonempty.parse(body.projectId)),
            resolution: { source: "manual", confidence: "high" },
          });
        const name = nonempty.parse(body.name),
          kind = z
            .enum(["normal", "external"])
            .default("normal")
            .parse(body.kind);
        const matches = (await store.list("project")).filter(
          (p) => String(p.name).toLowerCase() === name.toLowerCase(),
        );
        requireValue(
          matches.length <= 1,
          "PROJECT_IDENTITY_AMBIGUOUS",
          "Select an existing project by projectId.",
          409,
        );
        const project =
          matches[0] ??
          (await store.atomic(async () => {
            const p = await projectCreate(name, kind);
            await store.audit(
              "project.created",
              "project",
              p.id,
              actor,
              undefined,
              p,
            );
            return p;
          }));
        return response({
          project,
          resolution: { source: "manual", confidence: "high" },
        });
      }
      const projectRoute =
        /^\/projects\/([^/]+)(?:\/(settings|context))?$/.exec(route);
      if (projectRoute) {
        const project = await entity("project", projectRoute[1] ?? "");
        if (method === "GET" && projectRoute[2] === "context") {
          return response(
            await projectContext(store, {
              ...project,
              settings: settingsSchema.parse(project.settings),
            }),
          );
        }
        if (method === "GET")
          return response(
            projectRoute[2] === "settings"
              ? {
                  settings: settingsSchema.parse(project.settings),
                  version: project.version,
                }
              : {
                  project: {
                    ...project,
                    settings: settingsSchema.parse(project.settings),
                  },
                },
          );
        if (method === "PATCH" && !projectRoute[2])
          return response(
            await store.atomic(async () => {
              const changes = z
                .object({
                  name: nonempty.optional(),
                  kind: z.enum(["normal", "external"]).optional(),
                })
                .strict()
                .parse(
                  Object.fromEntries(
                    Object.entries(body).filter(
                      ([key]) => key !== "expectedVersion",
                    ),
                  ),
                );
              const settings =
                changes.kind === "external" && project.kind !== "external"
                  ? {
                      ...settingsSchema.parse(project.settings),
                      repoMirroring: {
                        ...settingsSchema.parse(project.settings).repoMirroring,
                        enabled: false,
                      },
                      crossProjectAnalysis: false,
                    }
                  : settingsSchema.parse(project.settings);
              const updated = await store.update(
                "project",
                { ...project, ...changes, settings },
                expected(body, request),
              );
              await store.audit(
                "project.updated",
                "project",
                project.id,
                actor,
                project,
                updated,
              );
              return { project: updated };
            }),
          );
        if (method === "PATCH" && projectRoute[2] === "settings")
          return response(
            await store.atomic(async () => {
              const settings = settingsSchema.parse(body.settings ?? body);
              const updated = await store.update(
                "project",
                { ...project, settings },
                expected(body, request),
              );
              await store.audit(
                "project.settings_updated",
                "project",
                project.id,
                actor,
                project,
                updated,
              );
              return { settings: updated.settings, version: updated.version };
            }),
          );
      }
      if (route === "/records" && method === "POST") {
        const projectId = nonempty.parse(body.projectId),
          record = captureSchema.parse(body.record);
        const requestKey = z
          .string()
          .min(1)
          .max(200)
          .optional()
          .parse(request.headers.get("Idempotency-Key") ?? undefined);
        const scope = `capture:${identity.credential.id}`,
          serialized = canonical({ projectId, record });
        const result = await store.atomic(async () => {
          if (requestKey) {
            const previous = await store.getRetry(scope, requestKey);
            if (previous) {
              requireValue(
                previous.request === serialized,
                "CONFLICT",
                "Idempotency key was used for a different capture.",
                409,
              );
              const replay = JSON.parse(previous.response) as JsonObject;
              const currentSettings = settingsSchema.parse(
                (await entity("project", projectId)).settings,
              );
              return {
                ...replay,
                mirror: {
                  eligible:
                    currentSettings.repoMirroring.enabled &&
                    currentSettings.repoMirroring.recordTypes.includes(
                      record.type,
                    ),
                },
              };
            }
          }
          const created = await createRecord(
            projectId,
            record,
            body.gitContext,
            actor,
          );
          if (requestKey)
            await store.saveRetry(
              scope,
              requestKey,
              serialized,
              JSON.stringify(created),
            );
          return created;
        });
        return response(result, 201);
      }
      if (route === "/records" && method === "GET")
        return response(await search(url.searchParams));
      if (route === "/search" && method === "POST") {
        const params = new URLSearchParams();
        if (body.query) params.set("q", z.string().max(2000).parse(body.query));
        for (const key of ["projectIds", "types"] as const)
          if (body[key])
            for (const value of z.array(z.string()).parse(body[key]))
              params.append(key, value);
        if (body.limit) params.set("limit", String(body.limit));
        if (body.cursor) params.set("cursor", z.string().parse(body.cursor));
        return response(await search(params));
      }
      const recordRoute =
        /^\/records\/([^/]+)(?:\/(metadata|revisions|evidence|mirror))?$/.exec(
          route,
        );
      if (recordRoute) {
        const record = await entity("record", recordRoute[1] ?? ""),
          operation = recordRoute[2];
        if (method === "GET" && !operation)
          return response(await recordDetail(record.id));
        if (
          (operation === "metadata" && method === "PATCH") ||
          (operation === "revisions" && method === "POST")
        ) {
          const metadata = await entity("metadata", record.id),
            changes = z
              .object({
                displayTitle: z.string().max(500).optional(),
                tags: z.array(z.string().max(100)).max(100).optional(),
                archived: z.boolean().optional(),
                curatedSummary: z.string().max(100_000).optional(),
              })
              .strict()
              .parse(
                operation === "revisions"
                  ? body.changes
                  : Object.fromEntries(
                      Object.entries(body).filter(
                        ([key]) => key !== "expectedVersion",
                      ),
                    ),
              );
          return response(
            await store.atomic(async () => {
              const updated = await store.update(
                "metadata",
                { ...metadata, ...changes },
                expected(body, request),
              );
              const revision = await store.insert("revision", {
                id: id("rev"),
                recordId: record.id,
                revisionNumber: updated.version,
                previous: metadata,
                next: updated,
                patch: changes,
                reason:
                  typeof body.reason === "string" ? body.reason : undefined,
                actor,
              });
              await store.audit(
                "record.curated",
                "record",
                record.id,
                actor,
                metadata,
                updated,
              );
              return { metadata: updated, revision };
            }),
          );
        }
        if (operation === "evidence" && method === "POST") {
          const evidence = z
            .object({
              kind: z.enum([
                "url",
                "git_commit",
                "pull_request",
                "issue",
                "file",
                "conversation",
                "test",
                "deployment",
                "other",
              ]),
              reference: z.string().min(1).max(4000),
              revision: z.string().max(500).optional(),
              description: z.string().max(10_000).optional(),
            })
            .parse(body);
          return response(
            await store.atomic(async () => {
              const saved = await store.insert("evidence", {
                ...evidence,
                id: id("evi"),
                recordId: record.id,
              });
              await store.audit(
                "evidence.created",
                "record",
                record.id,
                actor,
                undefined,
                saved,
              );
              return { evidence: saved };
            }),
            201,
          );
        }
        if (operation === "mirror" && method === "POST") {
          const state = z
            .object({
              attempted: z.boolean(),
              succeeded: z.boolean(),
              path: z.string().max(4000).optional(),
              error: z.string().max(4000).optional(),
            })
            .parse(body);
          const settings = settingsSchema.parse(
            (await entity("project", String(record.projectId))).settings,
          );
          requireValue(
            settings.repoMirroring.enabled &&
              settings.repoMirroring.recordTypes.includes(
                record.type as (typeof recordTypes)[number],
              ),
            "MIRROR_NOT_ALLOWED",
            "Project settings prohibit mirroring.",
            403,
          );
          return response(
            await store.atomic(async () => {
              const previous = await store.get("mirror", record.id);
              const next = {
                ...state,
                id: record.id,
                recordId: record.id,
                updatedAt: now(),
              };
              const mirror = previous
                ? await store.update("mirror", { ...previous, ...next })
                : await store.insert("mirror", next);
              await store.audit(
                "record.mirror_reported",
                "record",
                record.id,
                actor,
                previous,
                mirror,
              );
              return { mirror };
            }),
          );
        }
      }
      if (route === "/relationships" && method === "POST") {
        const data = z
          .object({
            fromRecordId: nonempty,
            toRecordId: nonempty,
            type: z.enum(relationshipTypes),
            authority: z
              .enum(["explicit", "inferred", "suggested"])
              .default("explicit"),
            note: z.string().max(10_000).optional(),
          })
          .parse(body);
        await entity("record", data.fromRecordId);
        await entity("record", data.toRecordId);
        requireValue(
          data.fromRecordId !== data.toRecordId,
          "INVALID_RELATIONSHIP",
          "A record cannot relate to itself.",
        );
        return response(
          await store.atomic(async () => {
            if (data.authority !== "suggested")
              await assertRelationshipSafe(
                store,
                data.fromRecordId,
                data.toRecordId,
                data.type,
              );
            const relationship = await store.insert("relationship", {
              ...data,
              id: id("rel"),
              status: data.authority === "suggested" ? "suggested" : "accepted",
            });
            await store.audit(
              "relationship.created",
              "relationship",
              relationship.id,
              actor,
              undefined,
              relationship,
            );
            return { relationship };
          }),
          201,
        );
      }
      const relRoute = /^\/relationships\/([^/]+)\/(accept|reject)$/.exec(
        route,
      );
      if (relRoute && method === "POST") {
        const previous = await entity("relationship", relRoute[1] ?? "");
        return response(
          await store.atomic(async () => {
            if (relRoute[2] === "accept")
              await assertRelationshipSafe(
                store,
                String(previous.fromRecordId),
                String(previous.toRecordId),
                String(previous.type),
                previous.id,
              );
            const relationship = await store.update(
              "relationship",
              {
                ...previous,
                status: relRoute[2] === "accept" ? "accepted" : "rejected",
                authority:
                  relRoute[2] === "accept" ? "explicit" : previous.authority,
              },
              expected(body, request),
            );
            await store.audit(
              `relationship.${relRoute[2]}`,
              "relationship",
              previous.id,
              actor,
              previous,
              relationship,
            );
            return { relationship };
          }),
        );
      }
      if (route === "/export" && method === "POST") {
        const data = Object.fromEntries(
          await Promise.all(
            exportKinds.map(async (kind) => [kind, await store.list(kind)]),
          ),
        );
        return response(
          { format: "scratchpad", version: 1, exportedAt: now(), data },
          200,
          {
            "Content-Disposition":
              'attachment; filename="scratchpad-export.json"',
          },
        );
      }
      if (route === "/import" && method === "POST")
        return response(
          body.format === "scratchpad"
            ? await importNative(store, body, actor)
            : await (async () => {
                requireValue(
                  body.format === "jsonl",
                  "IMPORT_INVALID",
                  "Use format scratchpad or jsonl.",
                );
                return await importLegacy(store, body, actor);
              })(),
        );
      if (route === "/settings" && method === "GET")
        return response({
          settings: {
            ...((await store.get("settings", "global")) ?? {
              id: "global",
              version: 0,
            }),
            aiEnabled: (await ai.settings()).enabled,
          },
        });
      if (route === "/settings" && method === "PATCH") {
        const changes = z
          .object({
            aiEnabled: z.boolean().optional(),
            defaultProjectSettings: settingsSchema.optional(),
          })
          .parse(body.settings);
        return response(
          await store.atomic(async () => {
            const previous = await store.get("settings", "global");
            if (!previous)
              requireValue(
                body.expectedVersion === 0,
                "CONFLICT",
                "Initial settings require expectedVersion 0.",
                409,
              );
            const settings = previous
              ? await store.update(
                  "settings",
                  { ...previous, ...changes },
                  expected(body, request),
                )
              : await store.insert("settings", { id: "global", ...changes });
            await store.audit(
              "settings.updated",
              "settings",
              "global",
              actor,
              previous,
              settings,
            );
            if (changes.aiEnabled !== undefined)
              await ai.configure(
                {
                  enabled: changes.aiEnabled,
                  expectedVersion: (await ai.settings()).version,
                },
                actor,
              );
            return {
              settings: {
                ...settings,
                aiEnabled: (await ai.settings()).enabled,
              },
            };
          }),
        );
      }
      throw new ApiError(404, "NOT_FOUND", "Endpoint not found.");
    } catch (error) {
      return publicError(error);
    }
  }
  return {
    store,
    auth,
    ai,
    github,
    handleRequest,
    close: async () => {
      await github.stop();
      await ai.stop();
      await store.close();
    },
  };
}
let singleton: ReturnType<typeof createApi> | undefined;
export function getApi(): ReturnType<typeof createApi> {
  singleton ??= createApi({
    githubOAuth:
      process.env.SCRATCHPAD_GITHUB_CLIENT_ID ||
      process.env.SCRATCHPAD_GITHUB_CLIENT_SECRET
        ? {
            clientId: process.env.SCRATCHPAD_GITHUB_CLIENT_ID ?? "",
            clientSecret: process.env.SCRATCHPAD_GITHUB_CLIENT_SECRET ?? "",
          }
        : undefined,
    databaseUrl: process.env.SCRATCHPAD_DATABASE_URL,
    pgvector: process.env.SCRATCHPAD_PGVECTOR === "true",
    trustedProxies: process.env.SCRATCHPAD_TRUSTED_PROXIES?.split(",")
      .map((value) => value.trim())
      .filter(Boolean),
    databasePath:
      process.env.SCRATCHPAD_DATABASE_PATH ?? "data/scratchpad.sqlite",
    origin: process.env.SCRATCHPAD_PUBLIC_URL ?? "http://localhost:3000",
  });
  singleton.github.start();
  singleton.ai.start();
  return singleton;
}
export const handleApiRequest = (
  request: Request,
  clientAddress?: string,
): Promise<Response> => getApi().handleRequest(request, clientAddress);
export const createSetupToken = async (recovery = false): Promise<string> =>
  await getApi().auth.createSetupToken(recovery);
