import { label, type Project, types } from "../lib/api";
import { type DashboardSearch, filterNames } from "../lib/dashboard";

export function MemoryFilters({
  search,
  projects,
  onApply,
}: {
  search: DashboardSearch;
  projects: Project[];
  onApply: (search: DashboardSearch) => void;
}) {
  const active = filterNames.filter((name) => search[name]);
  return (
    <>
      <form
        className="filters"
        onSubmit={(event) => {
          event.preventDefault();
          const form = new FormData(event.currentTarget);
          const next: DashboardSearch = {};
          for (const name of filterNames)
            next[name] = String(form.get(name) ?? "").trim() || undefined;
          onApply(next);
        }}
      >
        <div className="search-row">
          <label className="search-field">
            Search your memory
            <input
              name="q"
              placeholder="Search decisions, findings, questions…"
              defaultValue={search.q}
            />
          </label>
          <button type="submit" className="primary">
            Search
          </button>
        </div>
        <div className="filter-row">
          <label>
            Project
            <select
              name="projectId"
              aria-label="Project"
              defaultValue={search.projectId ?? ""}
              onChange={(event) => event.currentTarget.form?.requestSubmit()}
            >
              <option value="">All projects</option>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Record type
            <select
              name="type"
              aria-label="Record type"
              defaultValue={search.type ?? ""}
              onChange={(event) => event.currentTarget.form?.requestSubmit()}
            >
              <option value="">All types</option>
              {types.map((type) => (
                <option key={type} value={type}>
                  {label(type)}
                </option>
              ))}
            </select>
          </label>
          <details
            className="advanced-filters"
            open={
              active.some(
                (name) => !["q", "projectId", "type"].includes(name),
              ) || undefined
            }
          >
            <summary>More filters</summary>
            <div className="form-grid">
              <label>
                Tag
                <input name="tag" defaultValue={search.tag} />
              </label>
              <label>
                Git branch
                <input name="branch" defaultValue={search.branch} />
              </label>
              <label>
                From date (UTC)
                <input type="date" name="from" defaultValue={search.from} />
              </label>
              <label>
                Through date (UTC)
                <input type="date" name="to" defaultValue={search.to} />
              </label>
              <label>
                Lifecycle
                <select name="status" defaultValue={search.status ?? ""}>
                  <option value="">All statuses</option>
                  {[
                    "active",
                    "superseded",
                    "partially_superseded",
                    "archived",
                  ].map((value) => (
                    <option key={value} value={value}>
                      {label(value)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Record authority
                <select name="authority" defaultValue={search.authority ?? ""}>
                  <option value="">All authorities</option>
                  {[
                    "explicit",
                    "observed",
                    "inferred",
                    "derived",
                    "suggested",
                  ].map((value) => (
                    <option key={value} value={value}>
                      {label(value)}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <button type="submit">Apply filters</button>
          </details>
        </div>
      </form>
      {active.length > 0 && (
        <section className="active-filters" aria-label="Active filters">
          {active.map((name) => (
            <button
              type="button"
              key={name}
              aria-label={`Remove ${name === "projectId" ? "project" : name} filter`}
              onClick={() => onApply({ ...search, [name]: undefined })}
            >
              {name === "projectId"
                ? (projects.find((p) => p.id === search[name])?.name ??
                  "Project")
                : `${name === "q" ? "Search" : label(name)}: ${search[name]}`}{" "}
              ×
            </button>
          ))}
          <button
            type="button"
            className="text-button"
            onClick={() => onApply({})}
          >
            Clear filters
          </button>
        </section>
      )}
    </>
  );
}
