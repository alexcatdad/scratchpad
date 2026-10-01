/** Optional private-source acceptance. No source text, file paths, or original IDs are printed. */

import { spawnSync } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { once } from "node:events";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import { createApi } from "../apps/web/src/server/api";
import {
  canonical,
  type Entity,
  type JsonObject,
} from "../apps/web/src/server/domain";

function options(): Map<string, string> {
  const args = process.argv.slice(2),
    values = new Map<string, string>();
  const allowed = [
    "--usb-log",
    "--asource-log",
    "--usb-repo",
    "--stakeholder-record-id",
  ];
  for (let index = 0; index < args.length; index += 2) {
    const key = args[index],
      value = args[index + 1];
    if (!allowed.includes(key) || !value || value.startsWith("--"))
      throw new Error(
        "Usage: tsx scripts/validate-history.ts --usb-log <path> --asource-log <path> [--usb-repo <path>]",
      );
    values.set(key, value);
  }
  if (!values.has("--usb-log") || !values.has("--asource-log"))
    throw new Error("Both local JSONL paths are required.");
  return values;
}
const recordToken = (record: Entity) =>
  createHash("sha256").update(record.id).digest("hex").slice(0, 12);
const original = (record: Entity): JsonObject =>
  (record.payload as JsonObject).legacyOriginal as JsonObject;
const rationale = (row: JsonObject): boolean =>
  Boolean(row.rationale || row.why || row.reason);
function gitHistory(repo: string): JsonObject {
  const git = (args: string[]): string => {
    const result = spawnSync("git", ["-C", repo, ...args], {
      encoding: "utf8",
      maxBuffer: 16 * 1024 * 1024,
    });
    if (result.status !== 0)
      throw new Error("Could not inspect the requested local Git history.");
    return result.stdout;
  };
  const commits = git([
    "log",
    "--all",
    "--format=%H",
    "--",
    "decisions.jsonl",
    "docs",
    "README.md",
  ])
    .trim()
    .split("\n")
    .filter(Boolean);
  const matched = { throughput: 0, explicitPause: 0, membership: 0 };
  let inspected = 0;
  for (const commit of commits) {
    const paths = git(["ls-tree", "-r", "--name-only", commit])
      .split("\n")
      .filter(
        (path) =>
          path === "decisions.jsonl" ||
          path === "README.md" ||
          (path.startsWith("docs/") && path.endsWith(".md")),
      );
    for (const path of paths) {
      const text = git(["show", `${commit}:${path}`]);
      inspected++;
      if (/throughput|transfer[ -](?:speed|rate)/i.test(text))
        matched.throughput++;
      if (/\bpaused\b/i.test(text)) matched.explicitPause++;
      if (/membership|developer program/i.test(text)) matched.membership++;
    }
  }
  return {
    commitsInspected: commits.length,
    documentVersionsInspected: inspected,
    matchingDocumentVersions: matched,
    note: "Keyword coverage only. Membership mentions do not establish a pause or its reason.",
  };
}

async function main() {
  const args = options();
  const token = randomBytes(32).toString("base64url");
  let api: ReturnType<typeof createApi> | undefined;
  const server = createServer(async (request, response) => {
    try {
      const chunks: Buffer[] = [];
      for await (const chunk of request)
        chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      const headers = new Headers();
      for (const [key, value] of Object.entries(request.headers))
        if (value !== undefined)
          headers.set(key, Array.isArray(value) ? value.join(",") : value);
      if (!api) throw new Error("Isolated API is not ready.");
      const result = await api.handleRequest(
        new Request(`http://localhost${request.url}`, {
          method: request.method,
          headers,
          body: chunks.length ? Buffer.concat(chunks) : undefined,
        }),
      );
      response.writeHead(
        result.status,
        Object.fromEntries(result.headers.entries()),
      );
      response.end(Buffer.from(await result.arrayBuffer()));
    } catch {
      response.writeHead(500, { "Content-Type": "application/json" });
      response.end('{"error":{"code":"VALIDATION_TRANSPORT_FAILED"}}');
    }
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  if (!address || typeof address === "string")
    throw new Error("Unable to create isolated loopback transport.");
  const endpoint = `http://127.0.0.1:${address.port}`;
  api = createApi({ databasePath: ":memory:", origin: endpoint });
  // This credential belongs only to the disposable validation database, never a live instance.
  await api.store.insert("credential", {
    id: "isolated-validation",
    kind: "ssh",
  });
  await api.store.insert("session", {
    id: createHash("sha256").update(token).digest("hex"),
    credentialId: "isolated-validation",
    browser: false,
    expiresAt: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
  });
  const call = async (
    path: string,
    method = "GET",
    body?: unknown,
  ): Promise<JsonObject> => {
    const response = await fetch(`${endpoint}/api/v1${path}`, {
      method,
      headers: { Authorization: `Bearer ${token}` },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok)
      throw new Error(
        `Isolated HTTP acceptance failed with status ${response.status}.`,
      );
    return (await response.json()) as JsonObject;
  };
  const load = async (label: string, path: string) => {
    const text = readFileSync(path, "utf8");
    const project = (
      await call("/projects/resolve-explicit", "POST", {
        name: label,
        kind: "external",
      })
    ).project as Entity;
    const result = await call("/import", "POST", {
      format: "jsonl",
      projectId: project.id,
      jsonl: text,
      sourceName: "private-validation.jsonl",
    });
    const records = ((await api?.store.list("record")) ?? []).filter(
      (record) => record.projectId === project.id,
    );
    const lines = text.split(/\r?\n/);
    for (const record of records) {
      const source = (record.provenance as JsonObject).import as JsonObject;
      const rawLine = lines[Number(source.line) - 1];
      if (
        rawLine !== source.rawLine ||
        canonical(JSON.parse(rawLine)) !== canonical(original(record))
      )
        throw new Error("Imported record did not preserve supplied source.");
    }
    return {
      project,
      records,
      imported: result.imported,
      skipped: result.skipped,
    };
  };
  const search = async (
    projectId: string,
    queries: string[],
  ): Promise<Entity[]> => {
    const found = new Map<string, Entity>();
    for (const query of queries) {
      let cursor: unknown;
      do {
        const result = await call("/search", "POST", {
          query,
          projectIds: [projectId],
          limit: 100,
          ...(cursor ? { cursor } : {}),
        });
        for (const record of result.records as Entity[])
          found.set(record.id, record);
        cursor = result.nextCursor;
      } while (cursor);
    }
    return [...found.values()];
  };
  const inspect = async (records: Entity[]) => {
    let verified = 0,
      audits = 0,
      relationships = 0;
    for (const record of records) {
      const detail = await call(`/records/${encodeURIComponent(record.id)}`);
      if (
        canonical(original(detail.record as Entity)) !==
        canonical(original(record))
      )
        throw new Error("Record detail did not preserve original source.");
      verified++;
      audits += (detail.audit as unknown[]).length;
      relationships += (detail.relationships as unknown[]).length;
    }
    return {
      verifiedDetails: verified,
      auditEvents: audits,
      structuredRelationships: relationships,
    };
  };
  try {
    const usb = await load("technical-history", args.get("--usb-log") ?? "");
    const metadata = await search(usb.project.id, ["metadata"]);
    const throughput = await search(usb.project.id, [
      "throughput",
      "transfer speed",
      "transfer rate",
    ]);
    const resume = await search(usb.project.id, [
      "paused",
      "pause",
      "resume",
      "membership",
    ]);
    const context = await call(`/projects/${usb.project.id}/context`);
    const stateRows = usb.records.filter(
      (record) => record.type === "project_state",
    );
    const unresolved = usb.records.filter((record) => {
      const row = original(record);
      return Boolean(
        row.followUp ||
          row.followup ||
          row.remaining ||
          row.pending ||
          row.unresolved,
      );
    });
    const asource = await load(
      "stakeholder-history",
      args.get("--asource-log") ?? "",
    );
    const stakeholderCandidates = await search(asource.project.id, [
      "stakeholder",
      "stakeholders",
      "user requested",
      "customer request",
    ]);
    const withRationale = stakeholderCandidates.filter((record) =>
      rationale(original(record)),
    );
    let request: Entity | undefined,
      later: Entity[] = [];
    for (const candidate of withRationale) {
      const selectedId = args.get("--stakeholder-record-id");
      if (selectedId && original(candidate).id !== selectedId) continue;
      const sourceId = original(candidate).id;
      if (typeof sourceId !== "string") continue;
      const referenced = asource.records.filter(
        (record) =>
          record.id !== candidate.id &&
          String(record.happenedAt ?? "") >=
            String(candidate.happenedAt ?? "") &&
          JSON.stringify(
            original(record).supersession ?? original(record).supersedes ?? "",
          ).includes(sourceId),
      );
      if (referenced.length) {
        request = candidate;
        later = referenced;
        break;
      }
    }
    let relatedSearchMatches = 0;
    if (request) {
      const related = await search(asource.project.id, [
        String(original(request).id),
      ]);
      relatedSearchMatches = later.filter((record) =>
        related.some((match) => match.id === record.id),
      ).length;
    }
    const stakeholderContext = await call(
      `/projects/${asource.project.id}/context`,
    );
    const history = args.get("--usb-repo")
      ? gitHistory(args.get("--usb-repo") ?? "")
      : undefined;
    const result = {
      privacy: {
        disposableDatabase: true,
        loopbackHTTP: true,
        sourceTextPrinted: false,
        sourcePathsPrinted: false,
        originalIdsPrinted: false,
      },
      imports: {
        technical: { imported: usb.imported, skipped: usb.skipped },
        stakeholder: { imported: asource.imported, skipped: asource.skipped },
      },
      scenarioA: {
        status: throughput.length
          ? "requires_semantic_review"
          : "source_evidence_missing",
        metadataCandidates: metadata.length,
        throughputCandidates: throughput.length,
        ...(await inspect([
          ...new Map(
            [...metadata, ...throughput].map((record) => [record.id, record]),
          ).values(),
        ])),
        note: "Metadata hits alone do not prove the metadata-versus-throughput technical rationale.",
      },
      scenarioB: {
        status:
          stateRows.length && unresolved.length
            ? "requires_semantic_review"
            : "source_evidence_incomplete",
        resumeOrPauseCandidates: resume.length,
        explicitStateRecords: stateRows.length,
        structuredFollowupRecords: unresolved.length,
        currentStateAvailable: Boolean(context.currentState),
        ...(await inspect(resume)),
        note: "A historical resume statement is not proof of a currently paused state or its reason.",
      },
      scenarioC: {
        status:
          request && relatedSearchMatches
            ? "historical_chain_retrieved_requires_semantic_review"
            : "source_evidence_incomplete",
        stakeholderCandidates: stakeholderCandidates.length,
        candidatesWithRationale: withRationale.length,
        selectedRecordToken: request ? recordToken(request) : null,
        laterProseReferences: later.length,
        laterRecordsFoundByHTTPQuery: relatedSearchMatches,
        ...(await inspect(request ? [request, ...later] : [])),
        contextReviewCandidates: (
          stakeholderContext.requiresReview as unknown[]
        ).length,
        note: "Source request, rationale and later historical prose are retrievable; unstructured prose is not silently promoted to an accepted relationship.",
      },
      ...(history ? { technicalGitHistory: history } : {}),
      acceptance:
        "No overall MVP pass is inferred from keyword matches. Review source evidence locally; missing history must remain missing.",
    };
    console.log(JSON.stringify(result, null, 2));
  } finally {
    await new Promise<void>((resolve, reject) =>
      server.close((error) => (error ? reject(error) : resolve())),
    );
    await api.close();
  }
}
main().catch((error) => {
  console.error(
    error instanceof Error && error.message.startsWith("Usage:")
      ? error.message
      : "Private history validation failed. Inspect local paths and rerun; source content was not printed.",
  );
  process.exitCode = 1;
});
