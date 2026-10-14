import { z } from "zod";
import { isOpaqueId } from "../lib/opaque-id";
import { derivedArtifactSources } from "./derived-artifact";
import { ApiError, type Entity, settingsSchema } from "./domain";
import type { Store } from "./store";

const ids = z.array(z.string().refine(isOpaqueId)).min(1);
const schema = z.strictObject({
  version: z.literal(1),
  recordIds: ids,
  projectIds: ids,
  crossProject: z.boolean(),
});
export type PrivacyDependencies = z.infer<typeof schema>;

export function privacyDependencies(
  artifact: Record<string, unknown>,
): PrivacyDependencies | undefined {
  const result = schema.safeParse(artifact.privacyDependencies);
  const sources = ids.safeParse(artifact.sourceRecordIds);
  const projects = ids.safeParse(artifact.projectIds);
  if (!result.success || !sources.success || !projects.success) return;
  const dependencies = result.data;
  if (new Set(dependencies.projectIds).size > 1 && !dependencies.crossProject)
    return;
  const recordIds = new Set(dependencies.recordIds);
  const projectIds = new Set(dependencies.projectIds);
  if (
    !sources.data.every((id) => recordIds.has(id)) ||
    !projects.data.every((id) => projectIds.has(id))
  )
    return;
  return dependencies;
}

export function privacyAllowed(
  artifact: Record<string, unknown>,
  projects: Map<string, Entity>,
  records: Map<string, Entity>,
  requested?: string[],
): boolean {
  const dependencies = privacyDependencies(artifact);
  if (!dependencies) return false;
  const projectIds = new Set(dependencies.projectIds);
  return (
    dependencies.projectIds.every((id) => {
      const project = projects.get(id);
      if (!project || (requested && !requested.includes(id))) return false;
      const settings = settingsSchema.parse(project.settings);
      return (
        settings.aiProcessing &&
        (!dependencies.crossProject || settings.crossProjectAnalysis)
      );
    }) &&
    dependencies.recordIds.every((id) => {
      const record = records.get(id);
      return record && projectIds.has(String(record.projectId));
    })
  );
}

/** Unknown legacy provenance may be archived, never reconstructed from citations. */
export async function assertPrivacyReferences(
  store: Store,
  artifact: Record<string, unknown>,
  sources?: Awaited<ReturnType<typeof derivedArtifactSources>>,
): Promise<void> {
  if (artifact.privacyDependencies === undefined) return;
  const dependencies = privacyDependencies(artifact);
  if (!dependencies)
    throw new ApiError(
      400,
      "AI_PRIVACY_INVALID",
      "AI privacy dependencies are invalid.",
    );
  const snapshot = sources ?? (await derivedArtifactSources(store));
  const projects = new Set(dependencies.projectIds);
  for (const id of dependencies.projectIds)
    if (!snapshot.projects.has(id))
      throw new ApiError(
        400,
        "AI_PRIVACY_INVALID",
        "AI privacy dependencies reference a missing project.",
      );
  for (const id of dependencies.recordIds) {
    const record = snapshot.records.get(id);
    if (!record || !projects.has(String(record.projectId)))
      throw new ApiError(
        400,
        "AI_PRIVACY_INVALID",
        "AI privacy dependencies reference a missing or mismatched record.",
      );
  }
}
