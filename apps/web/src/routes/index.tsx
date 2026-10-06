import { createFileRoute } from "@tanstack/react-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { AuthScreen } from "../components/auth";
import { Capture } from "../components/capture";
import { Insights } from "../components/insights";
import { MemoryFilters } from "../components/memory-filters";
import { NavIcon } from "../components/nav-icon";
import { Projects } from "../components/projects";
import { type Detail, RecordDetail } from "../components/record-detail";
import { Settings } from "../components/settings";
import { api, label, type MemoryRecord, type Project, post } from "../lib/api";
import {
  type DashboardSearch,
  dashboardSearch,
  filterNames,
  recordParams,
  recordPreview,
  type View,
  views,
} from "../lib/dashboard";

export const Route = createFileRoute("/")({
  component: Dashboard,
  validateSearch: dashboardSearch,
});
function Dashboard() {
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const tab = search.view ?? "Memory";
  const recordId = search.recordId;
  const [status, setStatus] = useState<{
    initialized: boolean;
    authenticated: boolean;
  } | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [records, setRecords] = useState<MemoryRecord[]>([]);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [detailError, setDetailError] = useState("");
  const [capture, setCapture] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(false);
  const [cursor, setCursor] = useState<string | null>(null);
  const recordRequest = useRef(0);
  const detailRequest = useRef(0);
  const resultScroll = useRef(0);
  const lastRow = useRef<HTMLButtonElement | null>(null);
  const hadDetail = useRef(false);
  const params = recordParams(search).toString();
  const authenticate = useCallback(
    async () => setStatus(await api("/auth/status")),
    [],
  );
  const loadProjects = useCallback(async () => {
    setProjects((await api<{ projects: Project[] }>("/projects")).projects);
  }, []);
  const loadRecords = useCallback(
    async (next?: string) => {
      const request = ++recordRequest.current;
      setLoading(true);
      setError("");
      try {
        const query = new URLSearchParams(params);
        if (next) query.set("cursor", next);
        const data = await api<{
          records: MemoryRecord[];
          nextCursor: string | null;
        }>(`/records?${query}`);
        if (request !== recordRequest.current) return;
        setRecords((previous) =>
          next ? [...previous, ...data.records] : data.records,
        );
        setCursor(data.nextCursor);
      } catch (reason) {
        if (request === recordRequest.current)
          setError(
            reason instanceof Error
              ? reason.message
              : "Could not load records.",
          );
      } finally {
        if (request === recordRequest.current) setLoading(false);
      }
    },
    [params],
  );
  const loadDetail = useCallback(async () => {
    const request = ++detailRequest.current;
    if (!recordId) {
      setDetail(null);
      return;
    }
    setDetailError("");
    try {
      const data = await api<Detail>(
        `/records/${encodeURIComponent(recordId)}`,
      );
      if (request === detailRequest.current) setDetail(data);
    } catch (reason) {
      if (request === detailRequest.current)
        setDetailError(
          reason instanceof Error ? reason.message : "Could not open record.",
        );
    }
  }, [recordId]);
  const update = (patch: DashboardSearch) =>
    void navigate({
      search: (previous) => ({ ...previous, ...patch }),
      resetScroll: false,
    });
  const changeTab = (view: View) => {
    setNotice("");
    update({ view, recordId: undefined, section: undefined });
    window.scrollTo(0, 0);
  };
  const inspect = (id: string) => {
    if (!recordId) resultScroll.current = window.scrollY;
    update({ recordId: id });
  };
  const applyFilters = (next: DashboardSearch) => {
    const cleared = Object.fromEntries(
      filterNames.map((name) => [name, undefined]),
    );
    update({ ...cleared, ...next, view: "Memory", recordId: undefined });
    resultScroll.current = 0;
  };
  useEffect(() => {
    void authenticate().catch((e: Error) => setError(e.message));
  }, [authenticate]);
  useEffect(() => {
    if (status?.authenticated)
      void loadProjects().catch((e: Error) => setError(e.message));
  }, [status?.authenticated, loadProjects]);
  useEffect(() => {
    if (status?.authenticated) {
      setRecords([]);
      void loadRecords();
    }
    return () => {
      recordRequest.current++;
    };
  }, [status?.authenticated, loadRecords]);
  useEffect(() => {
    setDetail(null);
    if (status?.authenticated) void loadDetail();
    return () => {
      detailRequest.current++;
    };
  }, [status?.authenticated, loadDetail]);
  useEffect(() => {
    if (recordId) {
      hadDetail.current = true;
      window.scrollTo(0, 0);
    } else if (hadDetail.current) {
      hadDetail.current = false;
      window.scrollTo(0, resultScroll.current);
      lastRow.current?.focus({ preventScroll: true });
    }
  }, [recordId]);

  if (!status)
    return (
      <main className="auth-page">
        <div>
          <h1>Scratchpad</h1>
          <p role="status">{error || "Opening your memory…"}</p>
          {error && (
            <button
              type="button"
              onClick={() =>
                void authenticate().catch((e: Error) => setError(e.message))
              }
            >
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
  const selectedProject = projects.find((p) => p.id === search.projectId);
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <aside className="sidebar">
        <a href="/" className="wordmark">
          <span className="scratch" aria-hidden="true">
            ≋
          </span>
          Scratchpad
        </a>
        <nav aria-label="Main navigation">
          {views.map((name) => (
            <button
              key={name}
              type="button"
              className={tab === name ? "active" : ""}
              aria-current={tab === name ? "page" : undefined}
              onClick={() => changeTab(name)}
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
            onClick={() =>
              void api("/auth/logout", post({}))
                .then(authenticate)
                .catch((e: Error) => setError(e.message))
            }
          >
            <NavIcon name="Sign out" />
            Sign out
          </button>
        </div>
      </aside>
      <main className="main-pane" id="main-content">
        {notice && (
          <p role="status" className="notice">
            {notice}
          </p>
        )}
        {recordId ? (
          <>
            <div className="reader-toolbar">
              <button
                type="button"
                onClick={() => update({ recordId: undefined })}
              >
                Back to {tab === "Memory" ? "results" : tab.toLowerCase()}
              </button>
              <button type="button" onClick={() => setCapture(true)}>
                New record
              </button>
            </div>
            {detailError ? (
              <div role="alert" className="error">
                <p>{detailError}</p>
                <button type="button" onClick={() => void loadDetail()}>
                  Retry opening record
                </button>
              </div>
            ) : detail ? (
              <RecordDetail
                key={detail.record.id}
                detail={detail}
                projectName={
                  projects.find((p) => p.id === detail.record.projectId)?.name
                }
                onNavigate={inspect}
                onClose={() => update({ recordId: undefined })}
                onChanged={() => {
                  void loadDetail();
                  void loadRecords();
                }}
              />
            ) : (
              <p role="status">Opening record…</p>
            )}
          </>
        ) : null}
        <div hidden={Boolean(recordId)}>
          {tab === "Projects" ? (
            <Projects
              projects={projects}
              onChanged={() => void loadProjects()}
              onInspect={inspect}
              onOpen={(id) => applyFilters({ projectId: id })}
            />
          ) : tab === "Insights" ? (
            <Insights
              projects={projects}
              projectId={search.projectId ?? ""}
              onProjectChange={(projectId) =>
                update({ projectId: projectId || undefined })
              }
              section={search.section}
              onSectionChange={(section) => update({ section })}
              onSettings={() => update({ view: "Settings", section: "ai" })}
              onInspect={inspect}
            />
          ) : tab === "Settings" ? (
            <Settings
              projects={projects}
              section={search.section}
              onSectionChange={(section) => update({ section })}
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
                  <p>
                    {selectedProject
                      ? selectedProject.name
                      : "Decisions, findings, and the reasons behind your work."}
                  </p>
                </div>
                <button
                  className="primary"
                  type="button"
                  onClick={() =>
                    projects.length ? setCapture(true) : changeTab("Projects")
                  }
                >
                  New record
                </button>
              </header>
              <MemoryFilters
                key={`${params}:${projects.map((project) => project.id).join(",")}`}
                projects={projects}
                search={search}
                onApply={applyFilters}
              />
              <div className="results-toolbar">
                <span role="status">
                  {loading
                    ? "Loading records…"
                    : `${records.length}${cursor ? "+" : ""} records${selectedProject ? ` in ${selectedProject.name}` : ""}`}
                </span>
                <button
                  type="button"
                  className="text-button"
                  onClick={() =>
                    update({ view: "Insights", section: "search" })
                  }
                >
                  Search by meaning (AI)
                </button>
              </div>
              {error && (
                <div role="alert" className="error">
                  <p>{error}</p>
                  <button
                    type="button"
                    onClick={() => {
                      void loadProjects().catch((e: Error) =>
                        setError(e.message),
                      );
                      void loadRecords();
                    }}
                  >
                    Retry
                  </button>
                </div>
              )}
              <section aria-label="Records" aria-busy={loading}>
                <div className="list-heading">
                  <span>Type</span>
                  <span>Title</span>
                  <span>Project</span>
                </div>
                {records.map((record) => (
                  <button
                    key={record.id}
                    type="button"
                    className="memory-row"
                    onClick={(event) => {
                      lastRow.current = event.currentTarget;
                      inspect(record.id);
                    }}
                  >
                    <span className="record-type">{label(record.type)}</span>
                    <span>
                      <strong>
                        {record.metadata?.displayTitle || record.title}
                      </strong>
                      <small>{recordPreview(record)}</small>
                      <span className="record-tags">
                        {record.metadata?.tags?.map((tag) => (
                          <span key={tag}>#{tag}</span>
                        ))}
                      </span>
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
                      <small>{label(record.authority)}</small>
                    </span>
                  </button>
                ))}
                {!records.length && !loading && !error && (
                  <div className="empty">
                    <h2>
                      {filterNames.some((name) => search[name])
                        ? "No matching records."
                        : "Your next session starts here."}
                    </h2>
                    <p>
                      {filterNames.some((name) => search[name])
                        ? "Try another search, or clear your filters to see all records."
                        : "Capture a decision, finding, or question worth remembering."}
                    </p>
                    {filterNames.some((name) => search[name]) && (
                      <button type="button" onClick={() => applyFilters({})}>
                        Show all records
                      </button>
                    )}
                  </div>
                )}
                {cursor && (
                  <button
                    className="load-more"
                    type="button"
                    disabled={loading}
                    onClick={() => void loadRecords(cursor)}
                  >
                    {loading ? "Loading…" : "Load more"}
                  </button>
                )}
              </section>
            </>
          )}
        </div>
        {capture && (
          <Capture
            projects={projects}
            initialProjectId={detail?.record.projectId ?? search.projectId}
            onClose={() => setCapture(false)}
            onSaved={(projectId) => {
              setCapture(false);
              setNotice("Record saved.");
              void loadRecords();
              applyFilters({ projectId });
            }}
          />
        )}
      </main>
    </div>
  );
}
