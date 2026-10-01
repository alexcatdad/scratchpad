import { type Entity, type JsonObject, requireValue } from "./domain";
import type { Store } from "./store";
import { compareTimestamps } from "./timestamps";

export function assertRelationshipSafe(
  store: Store,
  from: string,
  to: string,
  type: string,
  excludeId?: string,
): void {
  requireValue(
    from !== to,
    "INVALID_RELATIONSHIP",
    "A record cannot relate to itself.",
  );
  if (!["replaces", "partially_replaces", "depends_on"].includes(type)) return;
  const types =
    type === "depends_on" ? ["depends_on"] : ["replaces", "partially_replaces"];
  const links = store
    .list("relationship")
    .filter(
      (r) =>
        r.id !== excludeId &&
        types.includes(String(r.type)) &&
        r.status === "accepted",
    );
  const pending = [to],
    seen = new Set<string>();
  while (pending.length) {
    const next = pending.pop();
    if (next === undefined) break;
    requireValue(
      next !== from,
      "RELATIONSHIP_CYCLE",
      "This relationship creates a cycle.",
      409,
    );
    if (seen.has(next)) continue;
    seen.add(next);
    for (const link of links)
      if (link.fromRecordId === next) pending.push(String(link.toRecordId));
  }
}
export function projectContext(store: Store, project: Entity): JsonObject {
  const relationships = store.list("relationship");
  const records = store
    .list("record")
    .filter((record) => record.projectId === project.id)
    .map((record): Entity & { applicability: string } => {
      const metadata = store.get("metadata", record.id),
        legacyStatus = metadata?.legacyStatus;
      const replacements = relationships.filter(
        (link) =>
          link.toRecordId === record.id &&
          link.status === "accepted" &&
          ["replaces", "partially_replaces"].includes(String(link.type)),
      );
      let applicability = "current";
      if (metadata?.archived) applicability = "archived";
      else if (replacements.some((link) => link.type === "replaces"))
        applicability = "superseded";
      else if (replacements.length) applicability = "partially_superseded";
      else if (legacyStatus === "superseded")
        applicability = "historically_superseded";
      else if (legacyStatus === "superseded_or_partial")
        applicability = "requires_review";
      else if ((record.provenance as JsonObject | undefined)?.import)
        applicability = "unverified";
      return {
        ...record,
        metadata,
        applicability,
        replacedBy: replacements.map((link) => ({
          recordId: link.fromRecordId,
          relationshipId: link.id,
          type: link.type,
          note: link.note,
        })),
        historicalStatus: legacyStatus ?? null,
      };
    })
    .sort(
      (a, b) =>
        compareTimestamps(
          String(b.happenedAt ?? b.recordedAt ?? b.createdAt),
          String(a.happenedAt ?? a.recordedAt ?? a.createdAt),
        ) ||
        compareTimestamps(b.createdAt, a.createdAt) ||
        b.id.localeCompare(a.id),
    );
  const available = records.filter((record) =>
    ["current", "unverified", "partially_superseded"].includes(
      record.applicability,
    ),
  );
  const types = (...values: string[]) =>
    available
      .filter((record) => values.includes(String(record.type)))
      .slice(0, 20);
  const states = types("project_state");
  return {
    project,
    currentState:
      states.find((record) => record.applicability === "current") ?? null,
    state: states,
    stateHistory: records
      .filter((record) => record.type === "project_state")
      .slice(0, 20),
    recentDecisions: types("decision", "adr", "business_decision"),
    constraints: types("constraint"),
    openFindings: types("finding"),
    failures: types("failure"),
    openQuestions: types("qa").filter(
      (record) => !(record.payload as JsonObject)?.answer,
    ),
    applicableRecords: available
      .filter((record) => record.applicability === "current")
      .slice(0, 30),
    partiallySuperseded: records
      .filter((record) => record.applicability === "partially_superseded")
      .slice(0, 20),
    historicalRecords: records
      .filter((record) =>
        ["archived", "superseded", "historically_superseded"].includes(
          record.applicability,
        ),
      )
      .slice(0, 20),
    requiresReview: records
      .filter((record) =>
        ["unverified", "requires_review"].includes(record.applicability),
      )
      .slice(0, 20),
    relationships: relationships
      .filter((link) =>
        records.some(
          (record) =>
            record.id === link.fromRecordId || record.id === link.toRecordId,
        ),
      )
      .slice(0, 100),
    counts: {
      total: records.length,
      current: records.filter((record) => record.applicability === "current")
        .length,
    },
    notice:
      "Deterministic context reflects recorded provenance and accepted relationships. Imported lifecycle statements remain historical evidence until reviewed.",
  };
}
