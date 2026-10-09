import { z } from "zod";
import { ApiError, type Entity, settingsSchema } from "./domain";
import type { Store } from "./store";

const ids = z.array(z.string().min(1).max(500)).min(1);
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
  if (
    !sources.data.every((id) => dependencies.recordIds.includes(id)) ||
    !projects.data.every((id) => dependencies.projectIds.includes(id))
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
      return (
        record && dependencies.projectIds.includes(String(record.projectId))
      );
    })
  );
}

/** Unknown legacy provenance may be archived, never reconstructed from citations. */
export async function assertPrivacyReferences(
  store: Store,
  artifact: Record<string, unknown>,
): Promise<void> {
  if (artifact.privacyDependencies === undefined) return;
  const dependencies = privacyDependencies(artifact);
  if (!dependencies)
    throw new ApiError(
      400,
      "AI_PRIVACY_INVALID",
      "AI privacy dependencies are invalid.",
    );
  for (const id of dependencies.projectIds)
    if (!(await store.get("project", id)))
      throw new ApiError(
        400,
        "AI_PRIVACY_INVALID",
        "AI privacy dependencies reference a missing project.",
      );
  for (const id of dependencies.recordIds) {
    const record = await store.get("record", id);
    if (!record || !dependencies.projectIds.includes(String(record.projectId)))
      throw new ApiError(
        400,
        "AI_PRIVACY_INVALID",
        "AI privacy dependencies reference a missing or mismatched record.",
      );
  }
}
