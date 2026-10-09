import { createHash } from "node:crypto";
import { z } from "zod";
import { assertRelationshipSafe } from "./context";
import { artifactKinds, assertDerivedArtifact } from "./derived-artifact";
import {
  type Actor,
  authorityTypes,
  canonical,
  type Entity,
  id,
  type JsonObject,
  now,
  recordTypes,
  relationshipTypes,
  requireValue,
  settingsSchema,
} from "./domain";
import type { Store } from "./store";

const object = z.record(z.string(), z.unknown());
const identifier = z.string().min(1).max(500);
export const exportKinds = [
  "project",
  "source",
  "record",
  "metadata",
  "revision",
  "relationship",
  "evidence",
  "mirror",
  "audit",
  "settings",
  "profile",
  "ai_artifact",
  "curated_artifact",
] as const;
const digest = (value: string) =>
  createHash("sha256").update(value).digest("hex");
const stringValue = (value: unknown): string | undefined =>
  typeof value === "string" && value.trim() ? value : undefined;
function readable(value: unknown): string | undefined {
  if (typeof value === "string") return value.trim() ? value : undefined;
  if (Array.isArray(value))
    return value.map(readable).filter(Boolean).join("\n") || undefined;
  return undefined;
}
async function requireEntity(
  store: Store,
  kind: string,
  value: unknown,
): Promise<Entity> {
  const entity = await store.get(kind, identifier.parse(value));
  requireValue(
    entity,
    kind === "project" ? "PROJECT_NOT_FOUND" : "IMPORT_INVALID",
    `Import references missing ${kind}.`,
    404,
  );
  return entity;
}
/** Knowledge restore preserves all source values. Authentication is restored through full database backups, not portable archives. */
export async function importNative(
  store: Store,
  body: JsonObject,
  actor: Actor,
): Promise<JsonObject> {
  requireValue(
    body.version === 1,
    "IMPORT_INVALID",
    "Unsupported export version.",
  );
  const data = object.parse(body.data);
  requireValue(
    Object.keys(data).every((key) =>
      exportKinds.includes(key as (typeof exportKinds)[number]),
    ),
    "IMPORT_INVALID",
    "Unknown export sections cannot be safely restored.",
  );
  return await store.atomic(async () => {
    let imported = 0,
      skipped = 0;
    for (const kind of exportKinds)
      for (const value of z.array(object).parse(data[kind] ?? [])) {
        const key = identifier.parse(value.id);
        z.iso.datetime().parse(value.createdAt);
        z.number().int().positive().parse(value.version);
        const previous = await store.get(kind, key);
        if (previous) {
          requireValue(
            canonical(previous) === canonical(value),
            "CONFLICT",
            `Import conflicts with existing ${kind} ${key}.`,
            409,
          );
          skipped++;
          continue;
        }
        if (kind === "project") {
          settingsSchema.parse(value.settings);
          z.string().min(1).parse(value.name);
          z.enum(["normal", "external"]).parse(value.kind);
        }
        if (kind === "profile") {
          requireValue(
            key === "owner-profile",
            "IMPORT_INVALID",
            "Unknown profile identity.",
          );
          z.string().trim().min(1).max(200).parse(value.displayName);
        }
        if (kind === "ai_artifact" || kind === "curated_artifact") {
          z.literal("derived").parse(value.authority);
          z.enum(artifactKinds).parse(value.kind);
          object.parse(value.content);
          object.parse(value.generator);
          await assertDerivedArtifact(store, value);
          const sources = z
            .array(identifier)
            .min(1)
            .parse(value.sourceRecordIds);
          for (const source of sources)
            await requireEntity(store, "record", source);
          const projects = z.array(identifier).min(1).parse(value.projectIds);
          for (const project of projects)
            await requireEntity(store, "project", project);
          if (kind === "ai_artifact")
            z.enum(["pending", "accepted", "rejected"]).parse(value.status);
          else await requireEntity(store, "ai_artifact", value.artifactId);
        }
        if (kind === "record") {
          await requireEntity(store, "project", value.projectId);
          z.enum(recordTypes).parse(value.type);
          z.string().parse(value.title);
          z.string().parse(value.content);
          object.parse(value.payload);
          z.enum(authorityTypes).nullable().parse(value.authority);
          z.iso.datetime().parse(value.recordedAt);
        }
        if (kind === "source") {
          await requireEntity(store, "project", value.projectId);
          identifier.parse(value.identity);
          const existing = await store.resolveIdentity(String(value.identity));
          requireValue(
            !existing || existing === value.projectId,
            "CONFLICT",
            "Project source identity belongs to another project.",
            409,
          );
        }
        if (["metadata", "revision", "evidence", "mirror"].includes(kind))
          await requireEntity(store, "record", value.recordId);
        if (kind === "metadata") {
          z.array(z.string()).parse(value.tags);
          z.boolean().optional().parse(value.archived);
          z.string().max(500).optional().parse(value.displayTitle);
          z.string().max(100000).optional().parse(value.curatedSummary);
          requireValue(
            value.id === value.recordId,
            "IMPORT_INVALID",
            "Metadata identity must match its record.",
          );
        }
        if (kind === "relationship") {
          await requireEntity(store, "record", value.fromRecordId);
          await requireEntity(store, "record", value.toRecordId);
          z.enum(relationshipTypes).parse(value.type);
          z.enum(["suggested", "accepted", "rejected"]).parse(value.status);
          z.enum(["explicit", "inferred", "suggested"]).parse(value.authority);
          requireValue(
            value.fromRecordId !== value.toRecordId,
            "IMPORT_INVALID",
            "A relationship cannot refer to itself.",
          );
        }
        await store.insert(kind, value as JsonObject & { id: string });
        imported++;
        if (kind === "record")
          await store.indexRecord(
            key,
            String(value.title),
            String(value.content),
          );
        if (kind === "source")
          await store.addIdentity(
            String(value.identity),
            String(value.projectId),
          );
      }
    for (const record of await store.list("record"))
      requireValue(
        await store.get("metadata", record.id),
        "IMPORT_INVALID",
        "Every restored record must include metadata.",
      );
    for (const relationship of await store.list("relationship"))
      if (relationship.status === "accepted")
        await assertRelationshipSafe(
          store,
          String(relationship.fromRecordId),
          String(relationship.toRecordId),
          String(relationship.type),
          relationship.id,
        );
    await store.audit(
      "data.imported",
      "import",
      id("import"),
      actor,
      undefined,
      {
        format: "scratchpad",
        imported,
        skipped,
      },
    );
    return { imported, skipped, warnings: [] };
  });
}

export async function importLegacy(
  store: Store,
  body: JsonObject,
  actor: Actor,
): Promise<JsonObject> {
  const projectId = identifier.parse(body.projectId);
  await requireEntity(store, "project", projectId);
  const sourceName = z
    .string()
    .min(1)
    .max(1000)
    .parse(
      body.sourceName ??
        (object.safeParse(body.source).success
          ? (body.source as JsonObject).filename
          : undefined) ??
        "legacy.jsonl",
    );
  const lines = z
    .string()
    .max(32 * 1024 * 1024)
    .parse(body.jsonl)
    .split(/\r?\n/);
  const warnings: JsonObject[] = [],
    results: JsonObject[] = [],
    occurrences = new Map<string, number>();
  const pending: { line: number; recordId: string; source: JsonObject }[] = [];
  let imported = 0,
    skipped = 0;
  const warn = (line: number, code: string, details?: unknown) =>
    warnings.push({ record: line, code, ...(details ? { details } : {}) });
  return await store.atomic(async () => {
    for (const [offset, rawLine] of lines.entries()) {
      const line = offset + 1;
      if (!rawLine.trim()) continue;
      let original: JsonObject;
      try {
        original = object.parse(JSON.parse(rawLine));
      } catch {
        warn(line, "INVALID_JSON");
        results.push({ line, status: "skipped" });
        skipped++;
        continue;
      }
      const originalId = stringValue(original.id),
        fingerprint = digest(canonical(original));
      const occurrence = occurrences.get(fingerprint) ?? 0;
      occurrences.set(fingerprint, occurrence + 1);
      const deterministic = `rec_import_${digest(canonical({ projectId, sourceName, identity: originalId ?? fingerprint, occurrence: originalId ? 0 : occurrence })).slice(0, 32)}`;
      let recordId =
        originalId && /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$/.test(originalId)
          ? originalId
          : deterministic;
      let existing = await store.get("record", recordId);
      if (existing && existing.projectId !== projectId) {
        warn(line, "ID_COLLISION", { originalId });
        recordId = deterministic;
        existing = await store.get("record", recordId);
      }
      if (existing) {
        const legacy = (existing.payload as JsonObject | undefined)
          ?.legacyOriginal;
        if (legacy && canonical(legacy) === canonical(original)) {
          skipped++;
          results.push({ line, recordId, status: "skipped" });
          pending.push({ line, recordId, source: original });
          continue;
        }
        warn(line, "IMPORT_ID_CONFLICT", { recordId });
        skipped++;
        results.push({ line, recordId, status: "conflict" });
        continue;
      }
      if (!originalId) warn(line, "MISSING_ID");
      else if (recordId !== originalId)
        warn(line, "ID_NOT_PRESERVED", { originalId, recordId });
      const requestedType = original.type ?? original.recordType ?? "decision";
      const parsedType = z.enum(recordTypes).safeParse(requestedType);
      const type = parsedType.success ? parsedType.data : "decision";
      if (!parsedType.success)
        warn(line, "UNSUPPORTED_RECORD_TYPE", { originalType: requestedType });
      const submitted = object.safeParse(original.payload);
      const payload: JsonObject = submitted.success
        ? { ...submitted.data }
        : {};
      const main =
        readable(original.decision) ??
        readable(original.content) ??
        readable(original.summary) ??
        readable(original.bodyMarkdown);
      const primary: Record<string, string> = {
        decision: "decision",
        adr: "decision",
        business_decision: "decision",
        finding: "finding",
        failure: "observed",
        constraint: "constraint",
        project_state: "state",
        qa: "question",
      };
      const primaryKey = primary[type] ?? "decision";
      if (payload[primaryKey] === undefined)
        payload[primaryKey] = readable(original[primaryKey]) ?? main;
      for (const key of [
        "question",
        "answer",
        "rationale",
        "expected",
        "observed",
        "cause",
        "resolution",
        "lesson",
        "reason",
        "scope",
        "previousState",
        "followUp",
        "requestedBy",
        "businessContext",
        "expectedOutcome",
        "context",
        "environment",
        "alternatives",
        "consequences",
        "limitations",
      ])
        if (payload[key] === undefined && original[key] !== undefined)
          payload[key] = original[key];
      if (payload.rationale === undefined)
        payload.rationale = readable(original.why) ?? readable(original.reason);
      if (
        payload.consequences === undefined &&
        original.implications !== undefined
      )
        payload.consequences = original.implications;
      if (!readable(payload[primaryKey])) {
        // Retain sparse but valid historical objects rather than discarding their evidence.
        warn(line, "MISSING_CONTENT");
      }
      if (
        ["decision", "adr", "business_decision"].includes(type) &&
        !readable(payload.rationale)
      )
        warn(line, "MISSING_RATIONALE");
      const authority = z.enum(authorityTypes).safeParse(original.authority);
      if (!authority.success) warn(line, "UNVERIFIED_LEGACY_PROVENANCE");
      const confidence = z
        .enum(["high", "medium", "low", "unknown"])
        .safeParse(original.confidence);
      const sourceDate =
        original.happenedAt ?? original.date ?? original.timestamp;
      let happenedAt: string | undefined;
      if (typeof sourceDate === "string") {
        if (z.iso.date().safeParse(sourceDate).success) {
          happenedAt = `${sourceDate}T00:00:00.000Z`;
        } else if (
          z.iso.datetime({ offset: true }).safeParse(sourceDate).success
        )
          happenedAt = new Date(sourceDate).toISOString();
        else warn(line, "UNSUPPORTED_DATE", { value: sourceDate });
      } else warn(line, "MISSING_DATE");
      const title = (
        stringValue(original.title) ??
        readable(payload[primaryKey])?.split("\n")[0] ??
        `Imported record ${line}`
      ).slice(0, 500);
      const content = Object.entries(original)
        .map(
          ([key, value]) =>
            `${key}: ${readable(value) ?? JSON.stringify(value)}`,
        )
        .join("\n\n");
      payload.legacyOriginal = original;
      const record = await store.insert("record", {
        id: recordId,
        projectId,
        type,
        title,
        content,
        payload,
        payloadVersion: 1,
        authority: authority.success ? authority.data : null,
        confidence: confidence.success ? confidence.data : "unknown",
        recordedAt: now(),
        happenedAt,
        actor: { ...actor, kind: "import", displayName: sourceName },
        gitContext: object.safeParse(original.gitContext).success
          ? original.gitContext
          : undefined,
        provenance: {
          import: {
            format: "jsonl",
            sourceName,
            line,
            originalId,
            rawLine,
            sha256: fingerprint,
            sourceDate,
            datePrecision:
              typeof sourceDate === "string" && sourceDate.length === 10
                ? "day"
                : happenedAt
                  ? "instant"
                  : "unknown",
            sourceActor: original.actor,
            source: original.source,
            authorityVerified: false,
          },
        },
      });
      await store.insert("metadata", {
        id: record.id,
        recordId: record.id,
        tags: [],
        archived: false,
        ...(typeof original.status === "string"
          ? { legacyStatus: original.status }
          : {}),
        updatedAt: now(),
      });
      await store.indexRecord(record.id, title, content);
      await store.audit(
        "record.imported",
        "record",
        record.id,
        { ...actor, kind: "import" },
        undefined,
        record,
      );
      if (original.evidence !== undefined)
        await store.insert("evidence", {
          id: id("evi"),
          recordId: record.id,
          kind: "other",
          reference: `${sourceName}:${line}`,
          description:
            "Unverified evidence preserved from the imported source.",
          sourceValue: original.evidence,
        });
      imported++;
      results.push({ line, recordId, status: "imported" });
      pending.push({ line, recordId, source: original });
    }
    const ids = new Map(
      pending.flatMap((item) =>
        typeof item.source.id === "string"
          ? [[item.source.id, item.recordId] as const]
          : [],
      ),
    );
    for (const item of pending) {
      const nested = object.safeParse(item.source.supersession);
      const supersession = nested.success ? nested.data : {};
      const refs = [
        { value: item.source.supersedes, reverse: false },
        {
          value:
            item.source.supersededBy ??
            item.source.superseded_by ??
            supersession.supersededBy,
          reverse: true,
        },
      ];
      if (item.source.supersession && !supersession.supersededBy)
        warn(item.line, "SUPERSESSION_REQUIRES_REVIEW");
      for (const { value, reverse } of refs) {
        if (value === undefined) continue;
        const references = Array.isArray(value) ? value : [value];
        for (const reference of references) {
          const target =
            typeof reference === "string"
              ? (ids.get(reference) ?? reference)
              : undefined;
          if (
            !target ||
            (await store.get("record", target))?.projectId !== projectId
          ) {
            warn(item.line, "UNRESOLVED_RELATIONSHIP", { reference });
            continue;
          }
          const fromRecordId = reverse ? target : item.recordId,
            toRecordId = reverse ? item.recordId : target;
          if (fromRecordId === toRecordId) {
            warn(item.line, "INVALID_RELATIONSHIP");
            continue;
          }
          const relationshipId = `rel_import_${digest(canonical({ fromRecordId, toRecordId, type: "replaces" })).slice(0, 32)}`;
          if (!(await store.get("relationship", relationshipId))) {
            // Imported references are unverified suggestions until the owner accepts them.
            const relationship = await store.insert("relationship", {
              id: relationshipId,
              fromRecordId,
              toRecordId,
              type: "replaces",
              authority: "suggested",
              status: "suggested",
              note: "Imported explicit reference; historical applicability requires review.",
              provenance: { sourceName, line: item.line },
            });
            await store.audit(
              "relationship.imported",
              "relationship",
              relationship.id,
              { ...actor, kind: "import" },
              undefined,
              relationship,
            );
          }
        }
      }
    }
    await store.audit(
      "data.imported",
      "import",
      id("import"),
      actor,
      undefined,
      {
        format: "jsonl",
        sourceName,
        imported,
        skipped,
      },
    );
    return { imported, skipped, warnings, records: results };
  });
}
