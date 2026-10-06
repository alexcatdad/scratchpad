import { type MemoryRecord, types } from "./api";

export const views = ["Memory", "Projects", "Insights", "Settings"] as const;
export type View = (typeof views)[number];
export const filterNames = [
  "projectId",
  "type",
  "q",
  "tag",
  "branch",
  "from",
  "to",
  "status",
  "authority",
] as const;
export type DashboardSearch = Partial<
  Record<(typeof filterNames)[number] | "recordId" | "section", string>
> & { view?: View };
export function dashboardSearch(
  input: Record<string, unknown>,
): DashboardSearch {
  const result: DashboardSearch = {};
  for (const key of [...filterNames, "recordId", "section"] as const) {
    if (typeof input[key] === "string" && input[key].trim())
      result[key] = input[key].trim();
  }
  if (views.includes(input.view as View)) result.view = input.view as View;
  if (result.type && !types.includes(result.type as (typeof types)[number]))
    delete result.type;
  for (const key of ["from", "to"] as const) {
    if (result[key] && !/^\d{4}-\d{2}-\d{2}$/.test(result[key]))
      delete result[key];
  }
  return result;
}
export function recordParams(search: DashboardSearch): URLSearchParams {
  const params = new URLSearchParams({ limit: "30" });
  for (const name of filterNames) {
    const value = search[name];
    if (value)
      params.set(
        name,
        name === "from"
          ? `${value}T00:00:00.000Z`
          : name === "to"
            ? `${value}T23:59:59.999Z`
            : value,
      );
  }
  return params;
}
export function recordPreview(record: MemoryRecord): string {
  const primary = [
    "decision",
    "finding",
    "question",
    "observed",
    "constraint",
    "state",
  ]
    .map((key) => record.payload[key])
    .find((value) => typeof value === "string" && value);
  return String(primary ?? record.content)
    .replace(/\s+/g, " ")
    .slice(0, 260);
}
