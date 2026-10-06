import { useCallback, useEffect, useRef, useState } from "react";
import { api, label, type MemoryRecord, type Project, post } from "../lib/api";

import { RecordContent } from "./record-content";

type Job = {
  id: string;
  version: number;
  type: string;
  status: string;
  attempts: number;
  lastError?: string;
  warnings?: { code: string; kind: string; count: number }[];
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
  projectId,
  onProjectChange,
  section,
  onSectionChange,
  onSettings,
  onInspect,
}: {
  projects: Project[];
  projectId: string;
  onProjectChange: (id: string) => void;
  section?: string;
  onSectionChange: (section: string) => void;
  onSettings: () => void;
  onInspect: (id: string) => void;
}) {
  const sections = [
    ["review", "Suggestions"],
    ["search", "Meaning search"],
    ["documents", "Documents"],
    ["jobs", "Background jobs"],
  ] as const;
  const selected = sections.some(([key]) => key === section)
    ? section
    : "review";
  const [showAllJobs, setShowAllJobs] = useState(false);
  const [aiEnabled, setAiEnabled] = useState(false);
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
    const [artifacts, queued, config] = await Promise.all([
      api<{ suggestions: Suggestion[] }>(`/suggestions?${params}`),
      api<{ jobs: Job[] }>("/ai/jobs"),
      api<{ enabled: boolean }>("/ai/settings"),
    ]);
    if (sequence !== requestSequence.current) return;
    setAiEnabled(config.enabled);
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
      <nav className="section-nav" aria-label="Insights sections">
        {sections.map(([key, name]) => (
          <button
            type="button"
            key={key}
            aria-current={selected === key ? "page" : undefined}
            onClick={() => {
              setNotice("");
              onSectionChange(key);
            }}
          >
            {name}
          </button>
        ))}
      </nav>
      {!aiEnabled && (
        <p className="notice">
          AI is disabled. Browsing and exact search still work.{" "}
          <button type="button" onClick={onSettings}>
            Configure AI provider
          </button>
        </p>
      )}
      <section className="insight-scope">
        <label>
          Insight project
          <select
            value={projectId}
            onChange={(event) => {
              setNotice("");
              setError("");
              onProjectChange(event.target.value);
            }}
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
        <div className="action-row" hidden={selected !== "review"}>
          <button
            type="button"
            disabled={busy || !aiEnabled || !participating.length}
            onClick={() => void perform(() => enqueue("analyze"))}
          >
            Analyze memory
          </button>
        </div>
      </section>
      <section className="settings-section" hidden={selected !== "search"}>
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
          <button
            type="submit"
            disabled={busy || !aiEnabled || !participating.length}
          >
            Search by meaning
          </button>
        </form>
        <details>
          <summary>Search index</summary>
          <p>
            Build or refresh the index after changing the embedding model. This
            sends eligible records to your configured provider.
          </p>
          <button
            type="button"
            disabled={busy || !aiEnabled || !participating.length}
            onClick={() => void perform(() => enqueue("embed"))}
          >
            Build embeddings
          </button>
        </details>
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
            <RecordContent value={record.content} />
          </article>
        ))}
      </section>
      <section className="settings-section" hidden={selected !== "documents"}>
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
            disabled={busy || !aiEnabled || !projectId || !participating.length}
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
      <section
        aria-label="AI suggestions"
        hidden={selected !== "review" && selected !== "documents"}
      >
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
        {suggestions
          .filter(
            (suggestion) =>
              selected !== "documents" || suggestion.kind === "export",
          )
          .map((suggestion) => (
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
              <RecordContent value={readable(suggestion.content)} />
              <small>
                Generated {new Date(suggestion.createdAt).toLocaleString("en")}{" "}
                · {suggestion.generator?.model ?? "Derived analysis"}
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
      <section className="settings-section" hidden={selected !== "jobs"}>
        <h2>Background jobs</h2>
        <p>
          {jobs.filter((job) => job.status === "failed").length} failed ·{" "}
          {
            jobs.filter((job) => ["queued", "running"].includes(job.status))
              .length
          }{" "}
          active · {jobs.length} recent jobs across projects.
        </p>
        {jobs.some((job) => job.status === "failed") && (
          <p className="notice">
            Check the saved provider connection and project permissions before
            retrying.{" "}
            <button type="button" onClick={onSettings}>
              Check AI settings
            </button>
          </p>
        )}
        {!jobs.length && <p>No jobs queued yet.</p>}
        <ul className="job-list">
          {(showAllJobs ? jobs : jobs.slice(0, 10)).map((job) => (
            <li key={job.id}>
              <strong>{label(job.type)}</strong>
              <small>{new Date(job.createdAt).toLocaleString("en")}</small>
              <span>
                {label(job.status)} · Attempt {job.attempts}
              </span>
              {job.lastError && <p className="error">{job.lastError}</p>}
              {job.warnings?.map((warning) => (
                <p key={`${warning.code}-${warning.kind}`}>
                  Skipped {warning.count} {label(warning.kind).toLowerCase()}
                  {warning.count === 1 ? " suggestion" : " suggestions"}:
                  insufficient supporting evidence.
                </p>
              ))}
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
        {jobs.length > 10 && (
          <button type="button" onClick={() => setShowAllJobs(!showAllJobs)}>
            {showAllJobs ? "Show recent jobs" : `Show all ${jobs.length} jobs`}
          </button>
        )}
      </section>
    </section>
  );
}
