import { useState } from "react";
import { api, type Project, post } from "../lib/api";
export function Projects({
  projects,
  onChanged,
}: {
  projects: Project[];
  onChanged: () => void;
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState("normal");
  const [error, setError] = useState("");
  async function create() {
    try {
      await api("/projects/resolve-explicit", post({ name, kind }));
      setName("");
      onChanged();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not create project.",
      );
    }
  }
  async function toggle(project: Project) {
    try {
      await api(`/projects/${project.id}/settings`, {
        method: "PATCH",
        headers: { "If-Match": String(project.version) },
        body: JSON.stringify({
          ...project.settings,
          repoMirroring: {
            ...project.settings.repoMirroring,
            enabled: !project.settings.repoMirroring.enabled,
          },
        }),
      });
      onChanged();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not update project.",
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
          <article key={project.id}>
            <div>
              <h2>{project.name}</h2>
              <p>
                {project.kind === "external"
                  ? "External / client"
                  : "Personal / internal"}
              </p>
              <code>{project.id}</code>
            </div>
            <label className="checkbox">
              <input
                type="checkbox"
                checked={project.settings.repoMirroring.enabled}
                onChange={() => void toggle(project)}
              />
              Allow repository mirroring
            </label>
          </article>
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
