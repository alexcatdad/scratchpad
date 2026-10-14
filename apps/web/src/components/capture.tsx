import { useEffect, useRef, useState } from "react";
import { api, label, type Project, post, types } from "../lib/api";

const fields: Record<string, string[]> = {
  decision: ["decision", "rationale"],
  adr: ["decision", "context", "rationale"],
  business_decision: ["decision", "rationale", "requestedBy"],
  finding: ["finding", "environment"],
  qa: ["question", "answer"],
  failure: ["observed", "expected", "lesson"],
  constraint: ["constraint", "reason", "scope"],
  project_state: ["state", "reason", "followUp"],
};
export function Capture({
  projects,
  initialProjectId,
  onClose,
  onSaved,
}: {
  projects: Project[];
  initialProjectId?: string;
  onClose: () => void;
  onSaved: (projectId: string) => void;
}) {
  const initialProject =
    projects.find((project) => project.id === initialProjectId) ?? projects[0];
  const initialTypes = initialProject?.settings.enabledRecordTypes ?? types;
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const element = dialog.current;
    const previous = document.activeElement as HTMLElement | null;
    element?.showModal();
    return () => {
      element?.close();
      previous?.focus();
    };
  }, []);
  const [type, setType] = useState(
    initialTypes.includes("finding") ? "finding" : (initialTypes[0] ?? ""),
  );
  const [projectId, setProjectId] = useState(initialProject?.id ?? "");
  const availableTypes: readonly string[] =
    projects.find((project) => project.id === projectId)?.settings
      .enabledRecordTypes ?? types;
  const [title, setTitle] = useState("");
  const [payload, setPayload] = useState<Record<string, string>>({});
  const [authority, setAuthority] = useState("explicit");
  const [confidence, setConfidence] = useState("unknown");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [requestId, setRequestId] = useState(() => crypto.randomUUID());
  function change() {
    setRequestId(crypto.randomUUID());
  }
  async function save() {
    setBusy(true);
    setError("");
    try {
      await api("/records", {
        ...post({
          projectId,
          record: {
            type,
            title,
            authority,
            confidence,
            payload,
            actor: { kind: "user", client: "browser" },
          },
        }),
        headers: { "Idempotency-Key": requestId },
      });
      onSaved(projectId);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not save record.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <dialog
      ref={dialog}
      onKeyDown={(event) => {
        if (event.key !== "Tab") return;
        const controls = Array.from(
          event.currentTarget.querySelectorAll<HTMLElement>(
            'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex="0"]',
          ),
        ).filter((control) => control.getClientRects().length > 0);
        const first = controls[0];
        const last = controls.at(-1);
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last?.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first?.focus();
        }
      }}
      onCancel={(event) => {
        event.preventDefault();
        if (!busy) onClose();
      }}
      aria-modal="true"
      aria-labelledby="capture-title"
      className="modal"
    >
      <header>
        <h2 id="capture-title">New record</h2>
        <button type="button" onClick={onClose} disabled={busy}>
          Close
        </button>
      </header>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
        onChange={change}
      >
        <div className="form-grid">
          <label>
            Project
            <select
              aria-label="Project"
              value={projectId}
              onChange={(e) => {
                setProjectId(e.target.value);
                const allowed: readonly string[] =
                  projects.find((project) => project.id === e.target.value)
                    ?.settings.enabledRecordTypes ?? types;
                if (!allowed.includes(type)) {
                  setType(allowed[0] ?? "");
                  setPayload({});
                }
              }}
              required
            >
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            Type
            <select
              aria-label="Type"
              value={type}
              onChange={(e) => {
                setType(e.target.value);
                setPayload({});
              }}
            >
              {availableTypes.map((t) => (
                <option key={t} value={t}>
                  {label(t)}
                </option>
              ))}
            </select>
          </label>
        </div>
        <label>
          Title
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            required
            maxLength={500}
          />
        </label>
        {fields[type]?.map((field, index) => (
          <label key={field}>
            {label(field)}
            {index > 0 && !(type === "qa" && field === "answer") && (
              <small>Optional</small>
            )}
            <textarea
              rows={3}
              value={payload[field] ?? ""}
              onChange={(e) =>
                setPayload({ ...payload, [field]: e.target.value })
              }
              required={index === 0 || type === "qa"}
            />
          </label>
        ))}
        <div className="form-grid">
          <label>
            Authority
            <select
              aria-label="Authority"
              value={authority}
              onChange={(e) => setAuthority(e.target.value)}
            >
              {["explicit", "observed", "inferred"].map((t) => (
                <option key={t} value={t}>
                  {label(t)}
                </option>
              ))}
            </select>
          </label>
          <label>
            Confidence
            <select
              aria-label="Confidence"
              value={confidence}
              onChange={(e) => setConfidence(e.target.value)}
            >
              {["unknown", "low", "medium", "high"].map((t) => (
                <option key={t} value={t}>
                  {label(t)}
                </option>
              ))}
            </select>
          </label>
        </div>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <button
          type="submit"
          className="primary"
          disabled={busy || !projectId || !availableTypes.includes(type)}
        >
          {busy ? "Saving…" : "Save record"}
        </button>
        {!projects.length && <p>Create a project first.</p>}
        {!availableTypes.length && (
          <p>
            Capture is disabled for this project. Enable a record type in
            project settings.
          </p>
        )}
      </form>
    </dialog>
  );
}
