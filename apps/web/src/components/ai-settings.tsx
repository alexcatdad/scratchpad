import { useCallback, useEffect, useState } from "react";
import { api, post } from "../lib/api";

export type AiConfiguration = {
  enabled: boolean;
  baseUrl: string;
  model: string;
  embeddingModel: string;
  embeddingDimensions: number;
  scheduleMinutes: number;
  similarityThreshold: number;
  analysisModes: string[];
  requestTimeoutSeconds: number;
  reasoningEffort: string;
  maxOutputTokens: number;
  apiKeyConfigured: boolean;
  version: number;
};

export function AiSettings() {
  const [configuration, setConfiguration] = useState<AiConfiguration | null>(
    null,
  );
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const refresh = useCallback(async () => {
    setConfiguration(await api<AiConfiguration>("/ai/settings"));
  }, []);
  useEffect(() => {
    void refresh().catch((reason: Error) => setError(reason.message));
  }, [refresh]);
  async function perform(work: () => Promise<void>) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await work();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Provider request failed.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="settings-section" aria-label="AI provider settings">
      <h2>Optional AI</h2>
      <p>
        Connect your provider for summaries, suggestions, and search by meaning.
        Choose participating projects in their settings. Original captures stay
        unchanged.
      </p>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {!configuration ? (
        <p role="status">Loading provider settings…</p>
      ) : (
        <form
          key={configuration.version}
          onSubmit={(event) => {
            event.preventDefault();
            const form = new FormData(event.currentTarget);
            void perform(async () => {
              const apiKey = String(form.get("apiKey") ?? "");
              setConfiguration(
                await api<AiConfiguration>("/ai/settings", {
                  method: "PATCH",
                  body: JSON.stringify({
                    enabled: form.get("enabled") === "on",
                    baseUrl: form.get("baseUrl"),
                    model: form.get("model"),
                    embeddingModel: form.get("embeddingModel"),
                    embeddingDimensions: Number(
                      form.get("embeddingDimensions"),
                    ),
                    scheduleMinutes: Number(form.get("scheduleMinutes")),
                    similarityThreshold: Number(
                      form.get("similarityThreshold"),
                    ),
                    analysisModes: form.getAll("analysisModes"),
                    requestTimeoutSeconds: Number(
                      form.get("requestTimeoutSeconds"),
                    ),
                    reasoningEffort: form.get("reasoningEffort"),
                    maxOutputTokens: Number(form.get("maxOutputTokens")),
                    ...(apiKey ? { apiKey } : {}),
                    expectedVersion: configuration.version,
                  }),
                }),
              );
              setNotice(
                "AI settings saved. Project participation is controlled separately.",
              );
            });
          }}
        >
          <label className="checkbox">
            <input
              type="checkbox"
              name="enabled"
              defaultChecked={configuration.enabled}
            />
            Enable AI processing
          </label>
          <label>
            OpenAI-compatible base URL
            <input
              type="url"
              name="baseUrl"
              required
              defaultValue={configuration.baseUrl}
            />
            <small>
              For LM Studio on this Mac, Docker uses
              http://host.docker.internal:1234/v1.
            </small>
          </label>
          <div className="form-grid">
            <label>
              LLM model
              <input name="model" required defaultValue={configuration.model} />
            </label>
            <label>
              Embedding model
              <input
                name="embeddingModel"
                required
                defaultValue={configuration.embeddingModel}
              />
            </label>
            <label>
              Embedding dimensions
              <input
                name="embeddingDimensions"
                type="number"
                min="1"
                max="16384"
                required
                defaultValue={configuration.embeddingDimensions}
              />
            </label>
            <label>
              Analysis interval (minutes)
              <input
                name="scheduleMinutes"
                type="number"
                min="0"
                max="43200"
                required
                defaultValue={configuration.scheduleMinutes}
              />
              <small>Use 0 for manual processing only.</small>
            </label>
            <label>
              Similarity threshold
              <input
                name="similarityThreshold"
                type="number"
                step="0.01"
                min="0"
                max="1"
                required
                defaultValue={configuration.similarityThreshold ?? 0.85}
              />
            </label>
            <label>
              Provider API key
              <input
                name="apiKey"
                type="password"
                autoComplete="new-password"
                placeholder={
                  configuration.apiKeyConfigured
                    ? "Key stored; leave blank to keep"
                    : "Optional for local providers"
                }
              />
              <small>
                Stored keys are never shown here or included in exports.
              </small>
            </label>
            <label>
              Request timeout (seconds)
              <input
                name="requestTimeoutSeconds"
                type="number"
                min="5"
                max="600"
                required
                defaultValue={configuration.requestTimeoutSeconds ?? 180}
              />
              <small>
                Allow enough time for your local model to finish a request.
              </small>
            </label>
            <label>
              Reasoning effort
              <select
                name="reasoningEffort"
                defaultValue={configuration.reasoningEffort ?? "none"}
              >
                <option value="default">Provider default</option>
                <option value="none">None</option>
                <option value="low">Low</option>
                <option value="medium">Medium</option>
                <option value="high">High</option>
                <option value="xhigh">Extra high</option>
              </select>
              <small>
                Choose provider default if your model does not support this
                option.
              </small>
            </label>
            <label>
              Maximum output tokens
              <input
                name="maxOutputTokens"
                type="number"
                min="256"
                max="32768"
                required
                defaultValue={configuration.maxOutputTokens ?? 4096}
              />
              <small>
                Longer outputs need more time, especially on local models.
              </small>
            </label>
          </div>
          <fieldset>
            <legend>Analysis types</legend>
            {[
              "summary",
              "classification",
              "duplicate_candidate",
              "relationship_candidate",
              "contradiction",
              "cluster",
              "pattern",
              "recommendation",
            ].map((mode) => (
              <label className="checkbox" key={mode}>
                <input
                  type="checkbox"
                  name="analysisModes"
                  value={mode}
                  defaultChecked={
                    configuration.analysisModes?.includes(mode) ?? true
                  }
                />
                {mode
                  .replaceAll("_", " ")
                  .replace(/^./, (letter) => letter.toUpperCase())}
              </label>
            ))}
          </fieldset>
          <div className="action-row">
            <button type="submit" className="primary" disabled={busy}>
              Save AI settings
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() =>
                void perform(async () => {
                  await api("/ai/test", post({}));
                  setNotice(
                    "Provider test passed using synthetic text for completion and embeddings.",
                  );
                })
              }
            >
              Test saved provider
            </button>
            {configuration.apiKeyConfigured && (
              <button
                type="button"
                disabled={busy}
                onClick={() =>
                  void perform(async () => {
                    setConfiguration(
                      await api<AiConfiguration>("/ai/settings", {
                        method: "PATCH",
                        body: JSON.stringify({
                          apiKey: "",
                          expectedVersion: configuration.version,
                        }),
                      }),
                    );
                    setNotice("Stored provider key removed.");
                  })
                }
              >
                Remove provider key
              </button>
            )}
          </div>
        </form>
      )}
    </section>
  );
}

export function OwnerSettings() {
  const [data, setData] = useState<{
    profile: { displayName: string; version: number };
    database: { engine: string };
  } | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  useEffect(() => {
    void api<typeof data>("/profile")
      .then(setData)
      .catch((reason: Error) => setError(reason.message));
  }, []);
  return (
    <section className="settings-section">
      <h2>Your profile and storage</h2>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {data && (
        <>
          <p>
            Database:{" "}
            {data.database.engine === "postgresql" ? "PostgreSQL" : "SQLite"}.
            Storage is selected in the server deployment configuration.
          </p>
          <form
            onSubmit={(event) => {
              event.preventDefault();
              const displayName = String(
                new FormData(event.currentTarget).get("displayName"),
              );
              setError("");
              void api<{ profile: { displayName: string; version: number } }>(
                "/profile",
                {
                  method: "PATCH",
                  body: JSON.stringify({
                    displayName,
                    expectedVersion: data.profile.version,
                  }),
                },
              )
                .then((result) => {
                  setData({ ...data, profile: result.profile });
                  setNotice("Profile saved.");
                })
                .catch((reason: Error) => setError(reason.message));
            }}
          >
            <label>
              Owner display name
              <input
                name="displayName"
                required
                maxLength={500}
                defaultValue={data.profile.displayName}
              />
            </label>
            <button type="submit">Save profile</button>
          </form>
        </>
      )}
    </section>
  );
}
