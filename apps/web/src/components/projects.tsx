import { useState } from "react";
import {
  api,
  label,
  type MemoryRecord,
  type Project,
  post,
  types,
} from "../lib/api";
export function Projects({
  projects,
  onChanged,
  onInspect,
  onOpen,
}: {
  projects: Project[];
  onChanged: () => void;
  onInspect: (id: string) => void;
  onOpen: (id: string) => void;
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState("normal");
  const [error, setError] = useState("");
  async function create() {
    try {
      await api("/projects/resolve-explicit", post({ name, kind }));
      setName("");
      setError("");
      onChanged();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not create project.",
      );
    }
  }
  return (
    <section>
      <header className="page-heading">
        <div>
          <h1>Projects</h1>
          <p>A place for every project's reasoning.</p>
        </div>
      </header>
      <form
        className="inline-form"
        onSubmit={(e) => {
          e.preventDefault();
          void create();
        }}
      >
        <label>
          Project name
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
            placeholder="My project"
          />
        </label>
        <label>
          Classification
          <select value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="normal">Personal / internal</option>
            <option value="external">External / client</option>
          </select>
        </label>
        <button type="submit" className="primary">
          Create project
        </button>
      </form>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="project-list">
        {projects.map((project) => (
          <ProjectCard
            key={`${project.id}-${project.version}`}
            project={project}
            onChanged={onChanged}
            onInspect={onInspect}
            onOpen={onOpen}
          />
        ))}
      </div>
      {!projects.length && (
        <div className="empty">
          <h2>Start with a project.</h2>
          <p>Create one here, or let MCP discover your Git repository.</p>
        </div>
      )}
    </section>
  );
}

function ProjectCard({
  project,
  onChanged,
  onInspect,
  onOpen,
}: {
  project: Project;
  onChanged: () => void;
  onInspect: (id: string) => void;
  onOpen: (id: string) => void;
}) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [context, setContext] = useState<{
    state: MemoryRecord[];
    recentDecisions: MemoryRecord[];
    constraints: MemoryRecord[];
    openFindings: MemoryRecord[];
    failures: MemoryRecord[];
  } | null>(null);
  async function save(path: string, body: unknown) {
    setError("");
    setBusy(true);
    try {
      await api(path, {
        method: "PATCH",
        headers: { "If-Match": String(project.version) },
        body: JSON.stringify(body),
      });
      onChanged();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not save settings.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <article className="project-card">
      <div>
        <h2>{project.name}</h2>
        <p>
          {project.kind === "external"
            ? "External / client"
            : "Personal / internal"}
        </p>
      </div>
      <div className="action-row">
        <button
          className="primary"
          type="button"
          onClick={() => onOpen(project.id)}
        >
          Open memory
        </button>
      </div>
      <details>
        <summary>Project settings</summary>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const form = new FormData(e.currentTarget);
            void save(`/projects/${project.id}/settings`, {
              ...project.settings,
              repoMirroring: {
                enabled: form.get("mirroring") === "on",
                recordTypes: form.getAll("mirrorTypes"),
              },
              crossProjectAnalysis: form.get("crossProject") === "on",
              aiProcessing: form.get("aiProcessing") === "on",
              enabledRecordTypes: form.getAll("enabledTypes"),
            });
          }}
        >
          <label className="checkbox">
            <input
              type="checkbox"
              name="mirroring"
              defaultChecked={project.settings.repoMirroring.enabled}
            />
            Allow repository mirroring
          </label>
          <fieldset>
            <legend>Record types to mirror</legend>
            {types.map((type) => (
              <label className="checkbox" key={type}>
                <input
                  type="checkbox"
                  name="mirrorTypes"
                  value={type}
                  defaultChecked={project.settings.repoMirroring.recordTypes.includes(
                    type,
                  )}
                />
                {label(type)}
              </label>
            ))}
          </fieldset>
          <label className="checkbox">
            <input
              type="checkbox"
              name="crossProject"
              defaultChecked={project.settings.crossProjectAnalysis}
            />
            Include in cross-project analysis
          </label>
          <label className="checkbox">
            <input
              type="checkbox"
              name="aiProcessing"
              defaultChecked={project.settings.aiProcessing}
            />
            Allow AI processing for this project
          </label>
          <p className="quiet">
            Enabling this sends this project's records to your configured AI
            provider. Cross-project analysis requires both permissions.
          </p>
          <fieldset>
            <legend>Enabled capture types</legend>
            {types.map((type) => (
              <label className="checkbox" key={type}>
                <input
                  type="checkbox"
                  name="enabledTypes"
                  value={type}
                  defaultChecked={
                    !project.settings.enabledRecordTypes ||
                    project.settings.enabledRecordTypes.includes(type)
                  }
                />
                {label(type)}
              </label>
            ))}
          </fieldset>
          <button type="submit" disabled={busy}>
            Save project settings
          </button>
        </form>
      </details>
      <details>
        <summary>Edit project</summary>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const form = new FormData(e.currentTarget);
            void save(`/projects/${project.id}`, Object.fromEntries(form));
          }}
        >
          <label>
            Project display name
            <input name="name" defaultValue={project.name} required />
          </label>
          <label>
            Project classification
            <select
              name="kind"
              aria-label="Project classification"
              defaultValue={project.kind}
            >
              <option value="normal">Personal / internal</option>
              <option value="external">External / client</option>
            </select>
          </label>
          <p>
            Review mirroring and cross-project settings after changing
            classification.
          </p>
          <button type="submit" disabled={busy}>
            Save project
          </button>
        </form>
      </details>
      <button
        type="button"
        onClick={() => {
          setError("");
          void api<typeof context>(`/projects/${project.id}/context`)
            .then(setContext)
            .catch((reason: Error) => setError(reason.message));
        }}
      >
        Show project context
      </button>
      {context && (
        <section aria-label={`${project.name} context`}>
          <h3>Project context</h3>
          {(
            [
              ["State", context.state],
              ["Decisions", context.recentDecisions],
              ["Constraints", context.constraints],
              ["Findings", context.openFindings],
              ["Failures and lessons", context.failures],
            ] as [string, MemoryRecord[]][]
          ).map(([title, records]) => (
            <section key={title}>
              <h4>{title}</h4>
              {records.length ? (
                records.map((record) => (
                  <p key={record.id}>
                    <button type="button" onClick={() => onInspect(record.id)}>
                      {record.title}
                    </button>
                    <small className="preserve">{record.content}</small>
                    {record.applicability &&
                      record.applicability !== "current" && (
                        <small>{label(record.applicability)}</small>
                      )}
                    {record.actor?.kind === "import" && (
                      <small>
                        Imported history; verify applicability before relying on
                        it.
                      </small>
                    )}
                  </p>
                ))
              ) : (
                <p className="quiet">None recorded.</p>
              )}
            </section>
          ))}
        </section>
      )}
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </article>
  );
}
