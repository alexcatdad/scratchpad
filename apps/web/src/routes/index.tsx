import { createFileRoute } from "@tanstack/react-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { AuthScreen } from "../components/auth";
import { Capture } from "../components/capture";
import { Insights } from "../components/insights";
import { NavIcon } from "../components/nav-icon";
import { Projects } from "../components/projects";
import { type Detail, RecordDetail } from "../components/record-detail";
import { Settings } from "../components/settings";
import {
  api,
  githubSignInError,
  label,
  type MemoryRecord,
  type Project,
  post,
  types,
} from "../lib/api";
export const Route = createFileRoute("/")({
  component: Dashboard,
  validateSearch: (search: Record<string, unknown>) => ({
    recordId: typeof search.recordId === "string" ? search.recordId : undefined,
    authError:
      typeof search.authError === "string" ? search.authError : undefined,
  }),
});
function Dashboard() {
  const { recordId, authError } = Route.useSearch();
  const authenticationError = githubSignInError(authError);
  const recordRequest = useRef(0);
  const detailRequest = useRef(0);
  const [status, setStatus] = useState<{
    initialized: boolean;
    authenticated: boolean;
    githubConfigured: boolean;
  } | null>(null);
  const [tab, setTab] = useState("Memory");
  const [projects, setProjects] = useState<Project[]>([]);
  const [records, setRecords] = useState<MemoryRecord[]>([]);
  const [project, setProject] = useState("");
  const [type, setType] = useState("");
  const [query, setQuery] = useState("");
  const [search, setSearch] = useState("");
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [detail, setDetail] = useState<Detail | null>(null);
  const [capture, setCapture] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [cursor, setCursor] = useState<string | null>(null);
  const authenticate = useCallback(async () => {
    setStatus(await api("/auth/status"));
  }, []);
  const loadProjects = useCallback(async () => {
    const data = await api<{ projects: Project[] }>("/projects");
    setProjects(data.projects);
  }, []);
  const loadRecords = useCallback(
    async (next?: string) => {
      const request = ++recordRequest.current;
      setLoading(true);
      setError("");
      try {
        const params = new URLSearchParams({ limit: "30" });
        if (project) params.set("projectId", project);
        if (type) params.set("type", type);
        if (search) params.set("q", search);
        if (next) params.set("cursor", next);
        for (const [name, value] of Object.entries(filters))
          if (value) params.set(name, value);
        const data = await api<{
          records: MemoryRecord[];
          nextCursor: string | null;
        }>(`/records?${params}`);
        if (request !== recordRequest.current) return;
        setRecords((previous) =>
          next ? [...previous, ...data.records] : data.records,
        );
        setCursor(data.nextCursor);
      } catch (reason) {
        if (request !== recordRequest.current) return;
        setError(
          reason instanceof Error ? reason.message : "Could not load records.",
        );
      } finally {
        if (request === recordRequest.current) setLoading(false);
      }
    },
    [project, type, search, filters],
  );
  const select = useCallback(async (record: MemoryRecord) => {
    const request = ++detailRequest.current;
    try {
      const data = await api<Detail>(
        `/records/${encodeURIComponent(record.id)}`,
      );
      if (request === detailRequest.current) setDetail(data);
    } catch (reason) {
      if (request === detailRequest.current)
        setError(
          reason instanceof Error ? reason.message : "Could not load record.",
        );
    }
  }, []);
  useEffect(() => {
    void authenticate().catch((e: Error) => setError(e.message));
  }, [authenticate]);
  useEffect(() => {
    if (status?.authenticated)
      void loadProjects().catch((e: Error) => setError(e.message));
  }, [status?.authenticated, loadProjects]);
  useEffect(() => {
    if (status?.authenticated) {
      detailRequest.current++;
      setDetail(null);
      setRecords([]);
      void loadRecords();
    }
  }, [status?.authenticated, loadRecords]);
  useEffect(() => {
    if (status?.authenticated && recordId) {
      setTab("Memory");
      void select({ id: recordId } as MemoryRecord);
    }
  }, [status?.authenticated, recordId, select]);
  if (!status)
    return (
      <main className="auth-page">
        <div>
          <h1>Scratchpad</h1>
          <p role="status">{error || "Opening your memory…"}</p>
          {error && (
            <button type="button" onClick={() => void authenticate()}>
              Retry
            </button>
          )}
        </div>
      </main>
    );
  if (!status.authenticated)
    return (
      <AuthScreen
        initialized={status.initialized}
        githubConfigured={status.githubConfigured}
        initialError={authenticationError}
        onAuthenticated={() => void authenticate()}
      />
    );
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a href="/" className="wordmark">
          <span className="scratch" aria-hidden="true">
            ≋
          </span>
          Scratchpad
        </a>
        <nav aria-label="Main navigation">
          {["Memory", "Projects", "Insights", "Settings"].map((name) => (
            <button
              key={name}
              type="button"
              className={tab === name ? "active" : ""}
              aria-current={tab === name ? "page" : undefined}
              onClick={() => setTab(name)}
            >
              <NavIcon name={name} />
              {name}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <a href="https://alexcatdad.github.io/scratchpad/">
            <NavIcon name="Documentation" />
            Documentation
          </a>
          <button
            type="button"
            onClick={() => {
              void api("/auth/logout", post({}))
                .then(authenticate)
                .catch((e: Error) => setError(e.message));
            }}
          >
            <NavIcon name="Sign out" />
            Sign out
          </button>
        </div>
      </aside>
      <main className="main-pane">
        {authenticationError && (
          <p role="alert" className="error">
            {authenticationError}
          </p>
        )}
        {tab === "Projects" ? (
          <Projects
            projects={projects}
            onChanged={() => void loadProjects()}
            onInspect={(id) => {
              setTab("Memory");
              void select({ id } as MemoryRecord);
            }}
          />
        ) : tab === "Insights" ? (
          <Insights
            projects={projects}
            onInspect={(id) => {
              setTab("Memory");
              void select({ id } as MemoryRecord);
            }}
          />
        ) : tab === "Settings" ? (
          <Settings
            projects={projects}
            onImported={() => {
              void loadProjects();
              void loadRecords();
            }}
          />
        ) : (
          <>
            <header className="page-heading">
              <div>
                <h1>Project memory</h1>
                <p>Decisions, findings, and the reasons behind your work.</p>
              </div>
              <button
                className="primary"
                type="button"
                onClick={() =>
                  projects.length ? setCapture(true) : setTab("Projects")
                }
              >
                New record
              </button>
            </header>
            <form
              className="filters"
              onSubmit={(e) => {
                e.preventDefault();
                setSearch(query);
                const form = new FormData(e.currentTarget);
                const next: Record<string, string> = {};
                for (const name of ["tag", "branch", "status", "authority"])
                  next[name] = String(form.get(name) ?? "");
                const from = String(form.get("from") ?? "");
                const to = String(form.get("to") ?? "");
                next.from = from ? `${from}T00:00:00.000Z` : "";
                next.to = to ? `${to}T23:59:59.999Z` : "";
                setFilters(next);
              }}
            >
              <input
                aria-label="Search your memory"
                placeholder="Search your memory"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <button type="submit">Search</button>
              <select
                aria-label="Project"
                value={project}
                onChange={(e) => setProject(e.target.value)}
              >
                <option value="">All projects</option>
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
              <select
                aria-label="Record type"
                value={type}
                onChange={(e) => setType(e.target.value)}
              >
                <option value="">All types</option>
                {types.map((t) => (
                  <option key={t} value={t}>
                    {label(t)}
                  </option>
                ))}
              </select>
              <details className="advanced-filters">
                <summary>More filters</summary>
                <div className="form-grid">
                  <label>
                    Tag
                    <input name="tag" defaultValue={filters.tag} />
                  </label>
                  <label>
                    Git branch
                    <input name="branch" defaultValue={filters.branch} />
                  </label>
                  <label>
                    From date (UTC)
                    <input
                      type="date"
                      name="from"
                      defaultValue={filters.from?.slice(0, 10)}
                    />
                  </label>
                  <label>
                    Through date (UTC)
                    <input
                      type="date"
                      name="to"
                      defaultValue={filters.to?.slice(0, 10)}
                    />
                  </label>
                  <label>
                    Lifecycle
                    <select
                      aria-label="Lifecycle"
                      name="status"
                      defaultValue={filters.status ?? ""}
                    >
                      <option value="">All statuses</option>
                      {[
                        "active",
                        "superseded",
                        "partially_superseded",
                        "archived",
                      ].map((value) => (
                        <option value={value} key={value}>
                          {label(value)}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    Record authority
                    <select
                      aria-label="Record authority"
                      name="authority"
                      defaultValue={filters.authority ?? ""}
                    >
                      <option value="">All authorities</option>
                      {[
                        "explicit",
                        "observed",
                        "inferred",
                        "derived",
                        "suggested",
                      ].map((value) => (
                        <option value={value} key={value}>
                          {label(value)}
                        </option>
                      ))}
                    </select>
                  </label>
                </div>
                <button type="submit">Apply filters</button>
              </details>
            </form>
            {error && (
              <p role="alert" className="error">
                {error}
              </p>
            )}
            <div className={detail ? "memory-grid" : "memory-grid no-detail"}>
              <section aria-label="Records">
                <div className="list-heading">
                  <span>Type</span>
                  <span>Title</span>
                  <span>Project</span>
                </div>
                {records.map((record) => (
                  <button
                    key={record.id}
                    type="button"
                    className={`memory-row ${detail?.record.id === record.id ? "selected" : ""}`}
                    onClick={() => void select(record)}
                  >
                    <span className="record-type">{label(record.type)}</span>
                    <span>
                      <strong>
                        {record.metadata?.displayTitle || record.title}
                      </strong>
                      <small>{record.content}</small>
                    </span>
                    <span className="project-name">
                      {projects.find((p) => p.id === record.projectId)?.name ??
                        "Project"}
                      <small>
                        {new Date(record.recordedAt).toLocaleDateString("en", {
                          month: "short",
                          day: "numeric",
                          year: "numeric",
                        })}
                      </small>
                    </span>
                  </button>
                ))}
                {loading && <p role="status">Loading records…</p>}
                {!records.length && !loading && (
                  <div className="empty">
                    <h2>
                      {search ||
                      project ||
                      type ||
                      Object.values(filters).some(Boolean)
                        ? "No matching records."
                        : "Your next session starts here."}
                    </h2>
                    <p>
                      {search ||
                      project ||
                      type ||
                      Object.values(filters).some(Boolean)
                        ? "Try a different search or filter."
                        : "Capture a decision, finding, or question worth remembering."}
                    </p>
                  </div>
                )}
                {cursor && (
                  <button
                    type="button"
                    disabled={loading}
                    onClick={() => void loadRecords(cursor)}
                  >
                    Load more
                  </button>
                )}
              </section>
              {detail && (
                <RecordDetail
                  key={detail.record.id}
                  detail={detail}
                  onNavigate={(id) => void select({ id } as MemoryRecord)}
                  onClose={() => setDetail(null)}
                  onChanged={() => {
                    void select(detail.record);
                    void loadRecords();
                  }}
                />
              )}
            </div>
          </>
        )}
        {capture && (
          <Capture
            projects={projects}
            onClose={() => setCapture(false)}
            onSaved={() => {
              setCapture(false);
              void loadRecords();
            }}
          />
        )}
      </main>
    </div>
  );
}
