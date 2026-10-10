import { z } from "zod";
import { ApiError, type Entity, relationshipTypes } from "./domain";
import type { Store } from "./store";

export const artifactKinds = [
  "summary",
  "classification",
  "duplicate_candidate",
  "relationship_candidate",
  "contradiction",
  "cluster",
  "pattern",
  "recommendation",
  "export",
] as const;
const reference = z.string().min(1).max(500);
const prose = {
  text: z.string().trim().min(1).max(300),
  markdown: z.string().optional(),
};
const endpoints = {
  fromRecordId: reference,
  toRecordId: reference,
};
export const aiRelationshipTypes = relationshipTypes.filter(
  (type) => type !== "replaces" && type !== "partially_replaces",
);
const ordinaryRelationships = z.enum(aiRelationshipTypes);
const contentSchemas = {
  summary: z.strictObject(prose),
  classification: z.strictObject({
    ...prose,
    tags: z.array(z.string().min(1).max(100)).max(20).optional(),
    classification: z.string().min(1).max(200).optional(),
  }),
  duplicate_candidate: z.strictObject(prose),
  relationship_candidate: z.strictObject({
    ...prose,
    ...endpoints,
    relationshipType: ordinaryRelationships.optional(),
  }),
  contradiction: z.strictObject({
    ...prose,
    ...endpoints,
    relationshipType: z.literal("contradicts").optional(),
  }),
  cluster: z.strictObject(prose),
  pattern: z.strictObject(prose),
  recommendation: z.strictObject(prose),
  export: z.strictObject({
    ...prose,
    markdown: z.string().trim().min(1),
  }),
};
const shape = z.object({
  kind: z.enum(artifactKinds),
  title: z.string().trim().min(1).max(500).optional(),
  sourceRecordIds: z.array(reference).min(1),
  content: z.record(z.string(), z.unknown()),
});

/** Validate without rewriting historical payloads or silently stripping fields. */
export function validDerivedArtifact(value: unknown): boolean {
  const parsed = shape.safeParse(value);
  if (!parsed.success) return false;
  const artifact = parsed.data;
  if (!contentSchemas[artifact.kind].safeParse(artifact.content).success)
    return false;
  const sources = new Set(artifact.sourceRecordIds);
  if (sources.size !== artifact.sourceRecordIds.length) return false;
  if (
    [
      "duplicate_candidate",
      "contradiction",
      "pattern",
      "relationship_candidate",
    ].includes(artifact.kind) &&
    sources.size < 2
  )
    return false;
  if (artifact.kind === "classification" && sources.size !== 1) return false;
  if (["relationship_candidate", "contradiction"].includes(artifact.kind)) {
    const { fromRecordId, toRecordId } = artifact.content;
    if (
      fromRecordId === toRecordId ||
      !sources.has(String(fromRecordId)) ||
      !sources.has(String(toRecordId))
    )
      return false;
  }
  return true;
}

export async function assertDerivedArtifact(
  store: Store,
  artifact: Entity | Record<string, unknown>,
): Promise<void> {
  const projects = z.array(reference).min(1).safeParse(artifact.projectIds);
  if (!validDerivedArtifact(artifact) || !projects.success)
    throw new ApiError(
      400,
      "AI_ARTIFACT_INVALID",
      "Derived artifact content or references are invalid.",
    );
  const knownProjects = new Set(
    (await store.list("project")).map((project) => project.id),
  );
  for (const projectId of projects.data)
    if (!knownProjects.has(projectId))
      throw new ApiError(
        400,
        "AI_ARTIFACT_INVALID",
        "Derived artifact references a missing project.",
      );
  const records = new Map(
    (await store.list("record")).map((record) => [record.id, record]),
  );
  const sourceProjects = new Set(projects.data);
  const citedProjects = new Set<string>();
  for (const recordId of artifact.sourceRecordIds as string[]) {
    const record = records.get(recordId);
    if (!record || !sourceProjects.has(String(record.projectId)))
      throw new ApiError(
        400,
        "AI_ARTIFACT_INVALID",
        "Derived artifact references an uncited or missing source project.",
      );
    citedProjects.add(String(record.projectId));
  }
  if (
    citedProjects.size !== projects.data.length ||
    !projects.data.every((project) => citedProjects.has(project))
  )
    throw new ApiError(
      400,
      "AI_ARTIFACT_INVALID",
      "Derived project IDs must uniquely match cited source projects.",
    );
}
