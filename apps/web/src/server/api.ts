import { z } from "zod";
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
import { exportKinds, importLegacy, importNative } from "./imports";
import { Store } from "./store";

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
    error instanceof Error ? error.message : "unknown error",
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
export function createApi(config: { databasePath: string; origin: string }) {
  const store = new Store(config.databasePath),
    auth = new Auth(store, config.origin);
  function entity(kind: string, key: string): Entity {
    const value = store.get(kind, key);
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
  function projectCreate(
    name: string,
    kind = "normal",
    identity?: string,
    sourceKind = "git_remote",
  ): Entity {
    const project = store.insert("project", {
      id: id("proj"),
      name,
      slug: name.toLowerCase().replace(/[^a-z0-9]+/g, "-"),
      kind,
      settings:
        kind === "external"
          ? defaults(kind)
          : settingsSchema.parse(
              store.get("settings", "global")?.defaultProjectSettings ??
                defaults(kind),
            ),
      updatedAt: now(),
    });
    if (identity) {
      store.sqlite
        .prepare("INSERT INTO identities VALUES(?,?)")
        .run(identity, project.id);
      store.insert("source", {
        id: id("source"),
        projectId: project.id,
        kind: sourceKind,
        identity,
      });
    }
    return project;
  }
  function createRecord(
    projectId: string,
    submitted: unknown,
    gitContext: unknown,
    actor: Actor,
  ): JsonObject {
    const project = entity("project", projectId),
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
    const saved = store.insert("record", {
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
    store.insert("metadata", {
      id: saved.id,
      recordId: saved.id,
      tags: [],
      archived: false,
      updatedAt: now(),
    });
    store.sqlite
      .prepare(
        "INSERT INTO record_search(record_id,title,content) VALUES(?,?,?)",
      )
      .run(saved.id, record.title, content);
    store.audit("record.created", "record", saved.id, actor, undefined, saved);
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
  function recordDetail(key: string): JsonObject {
    const relatedIds = new Set([
      key,
      ...store
        .list("relationship")
        .filter((link) => link.fromRecordId === key || link.toRecordId === key)
        .map((link) => link.id),
      ...store
        .list("evidence")
        .filter((item) => item.recordId === key)
        .map((item) => item.id),
    ]);
    return {
      record: entity("record", key),
      metadata: store.get("metadata", key),
      revisions: store.list("revision").filter((r) => r.recordId === key),
      relationships: store
        .list("relationship")
        .filter((r) => r.fromRecordId === key || r.toRecordId === key),
      evidence: store.list("evidence").filter((r) => r.recordId === key),
      mirror: store.get("mirror", key),
      audit: store
        .list("audit")
        .filter((r) => relatedIds.has(String(r.entityId))),
    };
  }
  function search(params: URLSearchParams): JsonObject {
    const limit = z.coerce
      .number()
      .int()
      .min(1)
      .max(100)
      .parse(params.get("limit") ?? 30);
    let records = store.list("record");
    const q = params.get("q");
    if (q) {
      const terms = q.match(/[\p{L}\p{N}_]+/gu)?.slice(0, 30) ?? [];
      if (!terms.length) records = [];
      else {
        const matching = store.sqlite
          .prepare(
            "SELECT record_id FROM record_search WHERE record_search MATCH ?",
          )
          .all(
            terms
              .map((term) => `"${term.replaceAll('"', '""')}"`)
              .join(" AND "),
          ) as { record_id: string }[];
        const ids = new Set(matching.map((r) => r.record_id));
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
        (store.get("metadata", r.id)?.tags as string[] | undefined)?.includes(
          tag,
        ),
      );
    if (branch)
      records = records.filter(
        (r) => (r.gitContext as JsonObject | undefined)?.branch === branch,
      );
    if (from) {
      z.iso.datetime().parse(from);
      records = records.filter(
        (r) => String(r.happenedAt ?? r.recordedAt) >= from,
      );
    }
    if (to) {
      z.iso.datetime().parse(to);
      records = records.filter(
        (r) => String(r.happenedAt ?? r.recordedAt) <= to,
      );
    }
    const status = params.get("status"),
      relationshipType = params.get("relationship"),
      relatedTo = params.get("relatedTo"),
      source = params.get("source"),
      gitPath = params.get("gitPath");
    if (status)
      records = records.filter((record) => {
        const metadata = store.get("metadata", record.id);
        const replacements = store
          .list("relationship")
          .filter(
            (link) =>
              link.toRecordId === record.id && link.status === "accepted",
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
      const links = store
        .list("relationship")
        .filter(
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
          Boolean(store.get("metadata", r.id)?.archived) ===
          (archived === "true"),
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
        metadata: store.get("metadata", r.id),
      })),
      nextCursor:
        records.length > limit && last
          ? Buffer.from(JSON.stringify([last.createdAt, last.id])).toString(
              "base64url",
            )
          : null,
    };
  }
  async function handleRequest(request: Request): Promise<Response> {
    try {
      const url = new URL(request.url),
        path = url.pathname.replace(/\/$/, "") || "/",
        method = request.method;
      if (method === "GET" && path === "/health")
        return response({ status: "ok" });
      if (method === "GET" && path === "/ready") {
        store.sqlite
          .prepare("SELECT version FROM schema_migrations WHERE version=1")
          .get();
        return response({ status: "ready", database: "sqlite" });
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
          auth.identify(request);
          authenticated = true;
        } catch {
          /* Status intentionally supports signed-out clients. */
        }
        return response({ initialized: auth.initialized(), authenticated });
      }
      if (route.startsWith("/auth/") && method === "POST") auth.throttle();
      if (route.startsWith("/auth/")) {
        if (method === "POST" && route === "/auth/mcp/challenge")
          return response(auth.sshChallenge(body));
        if (method === "POST" && route === "/auth/mcp/verify")
          return response(auth.sshVerify(body));
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
            identity = auth.identify(request);
          if (route === "/auth/register/options")
            return response(await auth.registrationOptions(body, identity));
          if (route === "/auth/login/options")
            return response(await auth.loginOptions());
          const session =
            route === "/auth/register/verify"
              ? await auth.registrationVerify(body, identity)
              : await auth.loginVerify(body);
          return response(
            { authenticated: true, expiresAt: session.expiresAt },
            200,
            { "Set-Cookie": auth.cookie(session.accessToken) },
          );
        }
      }
      const identity = auth.identify(request),
        actor = identity.actor;
      if (route === "/auth/logout" && method === "POST") {
        auth.logout(identity);
        return response({ authenticated: false }, 200, {
          "Set-Cookie": auth.clearCookie(),
        });
      }
      if (route === "/auth/credentials" && method === "GET")
        return response({ credentials: auth.publicCredentials() });
      if (route.startsWith("/auth/credentials")) {
        requireValue(
          identity.browser,
          "AUTH_INVALID",
          "Manage credentials from an authenticated browser.",
          403,
        );
        if (method === "POST" && route === "/auth/credentials/challenge")
          return response(auth.sshChallenge(body, identity));
        if (method === "POST" && route === "/auth/credentials/verify")
          return response(auth.sshVerify(body, identity));
        if (method === "DELETE") {
          auth.revoke(route.split("/")[3] ?? "", identity);
          return response({ revoked: true });
        }
      }
      if (route === "/projects" && method === "GET")
        return response({
          projects: store.list("project").map((p) => ({
            ...p,
            settings: settingsSchema.parse(p.settings),
            sources: store.list("source").filter((s) => s.projectId === p.id),
          })),
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
        const project = store.atomic(() => {
          const row = store.sqlite
            .prepare("SELECT project_id FROM identities WHERE identity=?")
            .get(remote) as { project_id: string } | undefined;
          if (row) return entity("project", row.project_id);
          const created = projectCreate(
            remote.split("/").at(-1) ?? remote,
            "normal",
            remote,
          );
          store.audit(
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
            project: entity("project", nonempty.parse(body.projectId)),
            resolution: { source: "manual", confidence: "high" },
          });
        const name = nonempty.parse(body.name),
          kind = z
            .enum(["normal", "external"])
            .default("normal")
            .parse(body.kind);
        const matches = store
          .list("project")
          .filter((p) => String(p.name).toLowerCase() === name.toLowerCase());
        requireValue(
          matches.length <= 1,
          "PROJECT_IDENTITY_AMBIGUOUS",
          "Select an existing project by projectId.",
          409,
        );
        const project =
          matches[0] ??
          store.atomic(() => {
            const p = projectCreate(name, kind);
            store.audit(
              "project.created",
              "project",
              p.id,
              actor,
              undefined,
              p,
            );
            return p;
          });
        return response({
          project,
          resolution: { source: "manual", confidence: "high" },
        });
      }
      const projectRoute =
        /^\/projects\/([^/]+)(?:\/(settings|context))?$/.exec(route);
      if (projectRoute) {
        const project = entity("project", projectRoute[1] ?? "");
        if (method === "GET" && projectRoute[2] === "context") {
          return response(
            projectContext(store, {
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
            store.atomic(() => {
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
              const updated = store.update(
                "project",
                { ...project, ...changes, settings },
                expected(body, request),
              );
              store.audit(
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
            store.atomic(() => {
              const settings = settingsSchema.parse(body.settings ?? body);
              const updated = store.update(
                "project",
                { ...project, settings },
                expected(body, request),
              );
              store.audit(
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
        const result = store.atomic(() => {
          if (requestKey) {
            const previous = store.sqlite
              .prepare(
                "SELECT request,response FROM retries WHERE scope=? AND key=?",
              )
              .get(scope, requestKey) as
              | { request: string; response: string }
              | undefined;
            if (previous) {
              requireValue(
                previous.request === serialized,
                "CONFLICT",
                "Idempotency key was used for a different capture.",
                409,
              );
              const replay = JSON.parse(previous.response) as JsonObject;
              const currentSettings = settingsSchema.parse(
                entity("project", projectId).settings,
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
          const created = createRecord(
            projectId,
            record,
            body.gitContext,
            actor,
          );
          if (requestKey)
            store.sqlite
              .prepare("INSERT INTO retries VALUES(?,?,?,?)")
              .run(scope, requestKey, serialized, JSON.stringify(created));
          return created;
        });
        return response(result, 201);
      }
      if (route === "/records" && method === "GET")
        return response(search(url.searchParams));
      if (route === "/search" && method === "POST") {
        const params = new URLSearchParams();
        if (body.query) params.set("q", z.string().max(2000).parse(body.query));
        for (const key of ["projectIds", "types"] as const)
          if (body[key])
            for (const value of z.array(z.string()).parse(body[key]))
              params.append(key, value);
        if (body.limit) params.set("limit", String(body.limit));
        if (body.cursor) params.set("cursor", z.string().parse(body.cursor));
        return response(search(params));
      }
      const recordRoute =
        /^\/records\/([^/]+)(?:\/(metadata|revisions|evidence|mirror))?$/.exec(
          route,
        );
      if (recordRoute) {
        const record = entity("record", recordRoute[1] ?? ""),
          operation = recordRoute[2];
        if (method === "GET" && !operation)
          return response(recordDetail(record.id));
        if (
          (operation === "metadata" && method === "PATCH") ||
          (operation === "revisions" && method === "POST")
        ) {
          const metadata = entity("metadata", record.id),
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
            store.atomic(() => {
              const updated = store.update(
                "metadata",
                { ...metadata, ...changes },
                expected(body, request),
              );
              const revision = store.insert("revision", {
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
              store.audit(
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
            store.atomic(() => {
              const saved = store.insert("evidence", {
                ...evidence,
                id: id("evi"),
                recordId: record.id,
              });
              store.audit(
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
            entity("project", String(record.projectId)).settings,
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
            store.atomic(() => {
              const previous = store.get("mirror", record.id);
              const next = {
                ...state,
                id: record.id,
                recordId: record.id,
                updatedAt: now(),
              };
              const mirror = previous
                ? store.update("mirror", { ...previous, ...next })
                : store.insert("mirror", next);
              store.audit(
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
        entity("record", data.fromRecordId);
        entity("record", data.toRecordId);
        requireValue(
          data.fromRecordId !== data.toRecordId,
          "INVALID_RELATIONSHIP",
          "A record cannot relate to itself.",
        );
        return response(
          store.atomic(() => {
            if (data.authority !== "suggested")
              assertRelationshipSafe(
                store,
                data.fromRecordId,
                data.toRecordId,
                data.type,
              );
            const relationship = store.insert("relationship", {
              ...data,
              id: id("rel"),
              status: data.authority === "suggested" ? "suggested" : "accepted",
            });
            store.audit(
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
        const previous = entity("relationship", relRoute[1] ?? "");
        return response(
          store.atomic(() => {
            if (relRoute[2] === "accept")
              assertRelationshipSafe(
                store,
                String(previous.fromRecordId),
                String(previous.toRecordId),
                String(previous.type),
                previous.id,
              );
            const relationship = store.update(
              "relationship",
              {
                ...previous,
                status: relRoute[2] === "accept" ? "accepted" : "rejected",
                authority:
                  relRoute[2] === "accept" ? "explicit" : previous.authority,
              },
              expected(body, request),
            );
            store.audit(
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
          exportKinds.map((kind) => [kind, store.list(kind)]),
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
            ? importNative(store, body, actor)
            : (() => {
                requireValue(
                  body.format === "jsonl",
                  "IMPORT_INVALID",
                  "Use format scratchpad or jsonl.",
                );
                return importLegacy(store, body, actor);
              })(),
        );
      if (route === "/settings" && method === "GET")
        return response({
          settings: store.get("settings", "global") ?? {
            id: "global",
            version: 0,
            aiEnabled: false,
          },
        });
      if (route === "/settings" && method === "PATCH") {
        const changes = z
          .object({
            aiEnabled: z.literal(false),
            defaultProjectSettings: settingsSchema.optional(),
          })
          .parse(body.settings);
        return response(
          store.atomic(() => {
            const previous = store.get("settings", "global");
            if (!previous)
              requireValue(
                body.expectedVersion === 0,
                "CONFLICT",
                "Initial settings require expectedVersion 0.",
                409,
              );
            const settings = previous
              ? store.update(
                  "settings",
                  { ...previous, ...changes },
                  expected(body, request),
                )
              : store.insert("settings", { id: "global", ...changes });
            store.audit(
              "settings.updated",
              "settings",
              "global",
              actor,
              previous,
              settings,
            );
            return { settings };
          }),
        );
      }
      throw new ApiError(404, "NOT_FOUND", "Endpoint not found.");
    } catch (error) {
      return publicError(error);
    }
  }
  return { store, auth, handleRequest, close: () => store.close() };
}
let singleton: ReturnType<typeof createApi> | undefined;
export function getApi(): ReturnType<typeof createApi> {
  singleton ??= createApi({
    databasePath:
      process.env.SCRATCHPAD_DATABASE_PATH ?? "data/scratchpad.sqlite",
    origin: process.env.SCRATCHPAD_PUBLIC_URL ?? "http://localhost:3000",
  });
  return singleton;
}
export const handleApiRequest = (request: Request): Promise<Response> =>
  getApi().handleRequest(request);
export const createSetupToken = (recovery = false): string =>
  getApi().auth.createSetupToken(recovery);
