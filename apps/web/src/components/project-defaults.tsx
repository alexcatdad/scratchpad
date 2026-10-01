import { useEffect, useState } from "react";
import { api, label, type Project, types } from "../lib/api";

type GlobalSettings = {
  version: number;
  defaultProjectSettings?: Project["settings"];
};
const initial: Project["settings"] = {
  enabledRecordTypes: [...types],
  repoMirroring: {
    enabled: false,
    recordTypes: ["decision", "adr", "business_decision"],
  },
  crossProjectAnalysis: false,
  aiProcessing: false,
};

export function ProjectDefaults() {
  const [settings, setSettings] = useState<GlobalSettings | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    void api<{ settings: GlobalSettings }>("/settings")
      .then((result) => setSettings(result.settings))
      .catch((reason: Error) => setError(reason.message));
  }, []);
  const defaults = settings?.defaultProjectSettings ?? initial;
  return (
    <section className="settings-section" aria-label="New project defaults">
      <h2>New project defaults</h2>
      <p>
        Apply these settings to future personal and internal projects. External
        projects start with mirroring, AI and cross-project analysis disabled.
        Existing projects keep their own settings.
      </p>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {settings && (
        <form
          key={settings.version}
          onSubmit={(event) => {
            event.preventDefault();
            const form = new FormData(event.currentTarget);
            setError("");
            setNotice("");
            setBusy(true);
            void api<{ settings: GlobalSettings }>("/settings", {
              method: "PATCH",
              body: JSON.stringify({
                expectedVersion: settings.version,
                settings: {
                  defaultProjectSettings: {
                    enabledRecordTypes: form.getAll("enabledTypes"),
                    repoMirroring: {
                      enabled: form.get("mirroring") === "on",
                      recordTypes: form.getAll("mirrorTypes"),
                    },
                    aiProcessing: form.get("aiProcessing") === "on",
                    crossProjectAnalysis: form.get("crossProject") === "on",
                  },
                },
              }),
            })
              .then((result) => {
                setSettings(result.settings);
                setNotice("New project defaults saved.");
              })
              .catch((reason: Error) => setError(reason.message))
              .finally(() => setBusy(false));
          }}
        >
          <label className="checkbox">
            <input
              type="checkbox"
              name="mirroring"
              defaultChecked={defaults.repoMirroring.enabled}
            />
            Allow repository mirroring by default
          </label>
          <fieldset>
            <legend>Default mirror types</legend>
            {types.map((type) => (
              <label key={type} className="checkbox">
                <input
                  type="checkbox"
                  name="mirrorTypes"
                  value={type}
                  defaultChecked={defaults.repoMirroring.recordTypes.includes(
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
              name="aiProcessing"
              defaultChecked={defaults.aiProcessing}
            />
            Allow AI processing by default
          </label>
          <label className="checkbox">
            <input
              type="checkbox"
              name="crossProject"
              defaultChecked={defaults.crossProjectAnalysis}
            />
            Include new projects in cross-project analysis
          </label>
          <p className="quiet">
            AI permissions allow records to be sent to your configured provider
            when AI is enabled.
          </p>
          <fieldset>
            <legend>Default enabled capture types</legend>
            {types.map((type) => (
              <label key={type} className="checkbox">
                <input
                  type="checkbox"
                  name="enabledTypes"
                  value={type}
                  defaultChecked={(
                    defaults.enabledRecordTypes ?? types
                  ).includes(type)}
                />
                {label(type)}
              </label>
            ))}
          </fieldset>
          <button type="submit" disabled={busy}>
            Save project defaults
          </button>
        </form>
      )}
    </section>
  );
}
