import { createFileRoute } from "@tanstack/react-router";
import { useCallback, useEffect, useState } from "react";
import { AuthScreen } from "../components/auth";
import { Capture } from "../components/capture";
import { Projects } from "../components/projects";
import { Settings } from "../components/settings";
import {
  api,
  label,
  type MemoryRecord,
  type Project,
  post,
  types,
} from "../lib/api";
export const Route = createFileRoute("/")({ component: Dashboard });
type Detail = {
  record: MemoryRecord;
  metadata: { displayTitle?: string; tags?: string[]; version: number };
  revisions: { id: string; createdAt: string; reason?: string }[];
  evidence: {
    id: string;
    kind: string;
    reference: string;
    description?: string;
  }[];
};
function Dashboard() {
  const [status, setStatus] = useState<{
    initialized: boolean;
    authenticated: boolean;
  } | null>(null);
  const [tab, setTab] = useState("Memory");
  const [projects, setProjects] = useState<Project[]>([]);
  const [records, setRecords] = useState<MemoryRecord[]>([]);
  const [project, setProject] = useState("");
  const [type, setType] = useState("");
  const [query, setQuery] = useState("");
  const [search, setSearch] = useState("");
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
      setLoading(true);
      setError("");
      try {
        const params = new URLSearchParams({ limit: "30" });
        if (project) params.set("projectId", project);
        if (type) params.set("type", type);
        if (search) params.set("q", search);
        if (next) params.set("cursor", next);
        const data = await api<{
          records: MemoryRecord[];
          nextCursor: string | null;
        }>(`/records?${params}`);
        setRecords((previous) =>
          next ? [...previous, ...data.records] : data.records,
        );
        setCursor(data.nextCursor);
      } catch (reason) {
        setError(
          reason instanceof Error ? reason.message : "Could not load records.",
        );
      } finally {
        setLoading(false);
      }
    },
    [project, type, search],
  );
  useEffect(() => {
    void authenticate().catch((e: Error) => setError(e.message));
  }, [authenticate]);
  useEffect(() => {
    if (status?.authenticated)
      void loadProjects().catch((e: Error) => setError(e.message));
  }, [status?.authenticated, loadProjects]);
  useEffect(() => {
    if (status?.authenticated) {
      setDetail(null);
      void loadRecords();
    }
  }, [status?.authenticated, loadRecords]);
  async function select(record: MemoryRecord) {
    try {
      setDetail(await api<Detail>(`/records/${record.id}`));
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not load record.",
      );
    }
  }
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
          {["Memory", "Projects", "Settings"].map((name) => (
            <button
              key={name}
              type="button"
              className={tab === name ? "active" : ""}
              onClick={() => setTab(name)}
            >
              {name}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <a href="https://alexcatdad.github.io/scratchpad/">Documentation</a>
          <button
            type="button"
            onClick={() => {
              void api("/auth/logout", post({}))
                .then(authenticate)
                .catch((e: Error) => setError(e.message));
            }}
          >
            Sign out
          </button>
        </div>
      </aside>
      <main className="main-pane">
        {tab === "Projects" ? (
          <Projects projects={projects} onChanged={() => void loadProjects()} />
        ) : tab === "Settings" ? (
          <Settings />
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
                      {search || project || type
                        ? "No matching records."
                        : "Your next session starts here."}
                    </h2>
                    <p>
                      {search || project || type
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
                  detail={detail}
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
function RecordDetail({
  detail,
  onClose,
  onChanged,
}: {
  detail: Detail;
  onClose: () => void;
  onChanged: () => void;
}) {
  const record = detail.record;
  const [error, setError] = useState("");
  async function retitle(form: FormData) {
    try {
      await api(`/records/${record.id}/metadata`, {
        method: "PATCH",
        headers: { "If-Match": String(detail.metadata.version) },
        body: JSON.stringify({ displayTitle: form.get("displayTitle") }),
      });
      onChanged();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not update title.",
      );
    }
  }
  return (
    <aside className="detail">
      <button className="close-detail" type="button" onClick={onClose}>
        Close
      </button>
      <h2>{detail.metadata.displayTitle || record.title}</h2>
      <span className="record-type">{label(record.type)}</span>
      {Object.entries(record.payload).map(([key, value]) => (
        <section key={key}>
          <h3>{label(key)}</h3>
          <p className="preserve">
            {typeof value === "string" ? value : JSON.stringify(value)}
          </p>
        </section>
      ))}
      <dl className="provenance">
        <div>
          <dt>Authority</dt>
          <dd>{label(record.authority)}</dd>
        </div>
        <div>
          <dt>Confidence</dt>
          <dd>{label(record.confidence)}</dd>
        </div>
      </dl>
      <section>
        <h3>History</h3>
        <p>Captured {new Date(record.recordedAt).toLocaleString("en")}</p>
        {detail.revisions.map((revision) => (
          <p key={revision.id}>
            {new Date(revision.createdAt).toLocaleString("en")} ·{" "}
            {revision.reason || "Metadata updated"}
          </p>
        ))}
        <details>
          <summary>Edit display title</summary>
          <form
            key={record.id}
            onSubmit={(e) => {
              e.preventDefault();
              void retitle(new FormData(e.currentTarget));
            }}
          >
            <label>
              Display title
              <input
                name="displayTitle"
                defaultValue={detail.metadata.displayTitle || record.title}
                required
              />
            </label>
            <button type="submit">Save title</button>
          </form>
          <small>Original capture remains unchanged.</small>
        </details>
      </section>
      <section>
        <h3>Evidence</h3>
        {detail.evidence.length ? (
          detail.evidence.map((e) => (
            <p key={e.id}>
              {e.description ?? e.kind}:{" "}
              <span className="break-word">{e.reference}</span>
            </p>
          ))
        ) : (
          <p className="quiet">No evidence attached.</p>
        )}
      </section>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      <small className="break-word">{record.id}</small>
    </aside>
  );
}
