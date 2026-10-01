import { useCallback, useEffect, useRef, useState } from "react";
import { api, label, type MemoryRecord, type Project, post } from "../lib/api";

type Job = {
  id: string;
  version: number;
  type: string;
  status: string;
  attempts: number;
  lastError?: string;
  createdAt: string;
  runAfter?: string;
};
type Suggestion = {
  id: string;
  title?: string;
  kind: string;
  status?: string;
  version: number;
  sourceRecordIds: string[];
  projectId?: string;
  content: Record<string, unknown>;
  generator?: { provider?: string; model?: string };
  createdAt: string;
};
type SemanticResult = { record: MemoryRecord; score: number };

function readable(value: unknown): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value))
    return value.map(readable).filter(Boolean).join("\n");
  if (value && typeof value === "object")
    return Object.entries(value)
      .map(([name, item]) => `${label(name)}: ${readable(item)}`)
      .join("\n");
  return value == null ? "" : String(value);
}
function download(suggestion: Suggestion) {
  const markdown = String(suggestion.content.markdown ?? "");
  const url = URL.createObjectURL(
    new Blob([markdown], { type: "text/markdown;charset=utf-8" }),
  );
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `scratchpad-${suggestion.kind}-${suggestion.id}.md`;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function Insights({
  projects,
  onInspect,
}: {
  projects: Project[];
  onInspect: (id: string) => void;
}) {
  const [projectId, setProjectId] = useState("");
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [results, setResults] = useState<SemanticResult[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [exportKind, setExportKind] = useState("handoff");
  const requestSequence = useRef(0);
  const participating = projects.filter(
    (project) =>
      project.settings.aiProcessing &&
      (projectId
        ? project.id === projectId
        : project.settings.crossProjectAnalysis),
  );
  const refresh = useCallback(async () => {
    const sequence = ++requestSequence.current;
    const params = new URLSearchParams(
      projectId ? { projectId } : { crossProject: "true" },
    );
    const [artifacts, queued] = await Promise.all([
      api<{ suggestions: Suggestion[] }>(`/suggestions?${params}`),
      api<{ jobs: Job[] }>("/ai/jobs"),
    ]);
    if (sequence !== requestSequence.current) return;
    setSuggestions(artifacts.suggestions);
    setJobs(queued.jobs);
  }, [projectId]);
  useEffect(() => {
    setResults([]);
    void refresh().catch((reason: Error) => setError(reason.message));
    return () => {
      requestSequence.current++;
    };
  }, [refresh]);
  const pending = jobs.some((job) =>
    ["queued", "running"].includes(job.status),
  );
  useEffect(() => {
    if (!pending) return;
    const timer = setInterval(
      () => void refresh().catch((reason: Error) => setError(reason.message)),
      4000,
    );
    return () => clearInterval(timer);
  }, [pending, refresh]);
  async function perform(work: () => Promise<void>) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await work();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not process memory.",
      );
    } finally {
      setBusy(false);
    }
  }
  const scope = projectId
    ? { projectId }
    : {
        projectIds: participating.map((project) => project.id),
        crossProject: true,
      };
  async function enqueue(type: string) {
    await api("/ai/jobs", post({ type, ...scope }));
    setNotice(
      `${type === "embed" ? "Embedding" : "Analysis"} job queued. You can keep browsing while it runs.`,
    );
    await refresh();
  }
  return (
    <section>
      <header className="page-heading">
        <div>
          <h1>Insights</h1>
          <p>
            Find connections, review suggestions, and prepare your next handoff.
          </p>
        </div>
        <button
          type="button"
          disabled={busy}
          onClick={() => void perform(refresh)}
        >
          Refresh insights
        </button>
      </header>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      <section className="insight-scope">
        <label>
          Insight project
          <select
            value={projectId}
            onChange={(event) => setProjectId(event.target.value)}
          >
            <option value="">Across participating projects</option>
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </label>
        <p className="quiet">
          {projectId
            ? "This project must allow AI processing in its project settings."
            : "Only projects allowing both AI processing and cross-project analysis participate."}{" "}
          {participating.length} participating{" "}
          {participating.length === 1 ? "project" : "projects"}.
        </p>
        <div className="action-row">
          <button
            type="button"
            disabled={busy || !participating.length}
            onClick={() => void perform(() => enqueue("analyze"))}
          >
            Analyze memory
          </button>
          <button
            type="button"
            disabled={busy || !participating.length}
            onClick={() => void perform(() => enqueue("embed"))}
          >
            Build embeddings
          </button>
        </div>
      </section>
      <section className="settings-section">
        <h2>Search by meaning</h2>
        <p>
          Semantic results are derived matches. Your exact search in Memory
          remains available without AI.
        </p>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            const query = String(
              new FormData(event.currentTarget).get("query"),
            );
            void perform(async () => {
              const result = await api<{
                results: SemanticResult[];
                indexRequired?: boolean;
              }>("/search/semantic", post({ ...scope, query, limit: 20 }));
              setResults(result.results);
              setNotice(
                result.indexRequired
                  ? "Build embeddings for this scope with the current model before searching by meaning."
                  : `Found ${result.results.length} semantic matches.`,
              );
            });
          }}
        >
          <label>
            Meaning to search for
            <input
              name="query"
              placeholder="Why was a project paused?"
              required
              maxLength={10000}
            />
          </label>
          <button type="submit" disabled={busy || !participating.length}>
            Search by meaning
          </button>
        </form>
        {results.map(({ record, score }) => (
          <article className="insight-card" key={record.id}>
            <button type="button" onClick={() => onInspect(record.id)}>
              {record.title}
            </button>
            <small>
              {
                projects.find((project) => project.id === record.projectId)
                  ?.name
              }{" "}
              · Similarity {score.toFixed(3)}
            </small>
            <p className="preserve">{record.content}</p>
          </article>
        ))}
      </section>
      <section className="settings-section">
        <h2>Prepare a document</h2>
        <p>
          Generated documents stay private here. Download one when you choose to
          share it, and review its sources first.
        </p>
        <div className="action-row">
          <label>
            Document kind
            <select
              value={exportKind}
              onChange={(event) => setExportKind(event.target.value)}
            >
              <option value="handoff">Project handoff</option>
              <option value="architecture">Architecture summary</option>
              <option value="decisions">Decision report</option>
              <option value="client_history">Client-facing history</option>
              <option value="adr">ADR export</option>
            </select>
          </label>
          <button
            type="button"
            disabled={busy || !projectId || !participating.length}
            onClick={() =>
              void perform(async () => {
                await api(
                  "/summaries/export",
                  post({ projectId, format: exportKind }),
                );
                setNotice(
                  "Document generation queued. Review the generated document below when it completes.",
                );
                await refresh();
              })
            }
          >
            Generate document
          </button>
        </div>
        {!projectId && <small>Select one project to prepare a document.</small>}
      </section>
      <section aria-label="AI suggestions">
        <h2>Suggestions and documents</h2>
        <p className="quiet">
          AI output is derived knowledge. Accepting suggestions adds audited
          curation and preserves every original capture.
        </p>
        {!suggestions.length && (
          <p>
            No derived artifacts for this scope yet. Enable AI for a project and
            run analysis to get started.
          </p>
        )}
        {suggestions.map((suggestion) => (
          <article className="insight-card" key={suggestion.id}>
            <div className="action-row">
              <span className="record-type">{label(suggestion.kind)}</span>
              <span>{label(suggestion.status ?? "suggested")}</span>
            </div>
            <h3>
              {String(
                suggestion.title ??
                  suggestion.content.title ??
                  label(suggestion.kind),
              )}
            </h3>
            <p className="preserve">{readable(suggestion.content)}</p>
            <small>
              Generated {new Date(suggestion.createdAt).toLocaleString("en")} ·{" "}
              {suggestion.generator?.model ?? "Derived analysis"}
            </small>
            <div className="source-links">
              {suggestion.sourceRecordIds.map((id) => (
                <button type="button" key={id} onClick={() => onInspect(id)}>
                  Source {id.slice(-8)}
                </button>
              ))}
            </div>
            <div className="action-row">
              {(!suggestion.status ||
                ["suggested", "pending"].includes(suggestion.status)) &&
                ["accept", "reject"].map((action) => (
                  <button
                    key={action}
                    type="button"
                    disabled={busy}
                    onClick={() =>
                      void perform(async () => {
                        await api(
                          `/suggestions/${suggestion.id}/${action}`,
                          post({ expectedVersion: suggestion.version }),
                        );
                        await refresh();
                        setNotice(
                          action === "accept"
                            ? "Suggestion accepted with an audit entry."
                            : "Suggestion rejected.",
                        );
                      })
                    }
                  >
                    {action === "accept"
                      ? "Accept suggestion"
                      : "Reject suggestion"}
                  </button>
                ))}
              {typeof suggestion.content.markdown === "string" && (
                <button type="button" onClick={() => download(suggestion)}>
                  Download Markdown
                </button>
              )}
            </div>
          </article>
        ))}
      </section>
      <section className="settings-section">
        <h2>Background jobs</h2>
        {!jobs.length && <p>No jobs queued yet.</p>}
        <ul className="job-list">
          {jobs.map((job) => (
            <li key={job.id}>
              <strong>{label(job.type)}</strong>
              <span>
                {label(job.status)} · Attempt {job.attempts}
              </span>
              {job.lastError && <p className="error">{job.lastError}</p>}
              {job.status === "failed" && (
                <button
                  type="button"
                  disabled={busy}
                  onClick={() =>
                    void perform(async () => {
                      await api(
                        `/ai/jobs/${job.id}/retry`,
                        post({ expectedVersion: job.version }),
                      );
                      await refresh();
                      setNotice("Failed job queued for another attempt.");
                    })
                  }
                >
                  Retry job
                </button>
              )}
              {job.status === "queued" && job.runAfter && (
                <small>
                  Eligible after {new Date(job.runAfter).toLocaleString("en")}
                </small>
              )}
            </li>
          ))}
        </ul>
      </section>
    </section>
  );
}
