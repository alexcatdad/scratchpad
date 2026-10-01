import type { PublicKeyCredentialCreationOptionsJSON } from "@simplewebauthn/browser";
import { startRegistration } from "@simplewebauthn/browser";
import { useCallback, useEffect, useState } from "react";
import { api, type Project, post } from "../lib/api";
import { AiSettings, OwnerSettings } from "./ai-settings";
import { ProjectDefaults } from "./project-defaults";

type Credential = {
  id: string;
  kind: string;
  label?: string;
  revokedAt?: string;
  createdAt: string;
};
export function Settings({
  projects,
  onImported,
}: {
  projects: Project[];
  onImported: () => void;
}) {
  const [importProject, setImportProject] = useState(projects[0]?.id ?? "");
  const [importWarnings, setImportWarnings] = useState<
    { record?: number; code: string }[]
  >([]);
  async function importData(data: unknown) {
    const result = await api<{
      imported: number;
      skipped: number;
      warnings: { record?: number; code: string }[];
    }>("/import", post(data));
    setNotice(`Imported ${result.imported} items; skipped ${result.skipped}.`);
    setImportWarnings(result.warnings);
    onImported();
  }
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [challenge, setChallenge] = useState<{
    challengeId: string;
    nonce: string;
    namespace: string;
  } | null>(null);
  const [signature, setSignature] = useState("");
  const refresh = useCallback(async () => {
    const data = await api<{ credentials: Credential[] }>("/auth/credentials");
    setCredentials(data.credentials);
  }, []);
  useEffect(() => {
    void refresh().catch((e: Error) => setError(e.message));
  }, [refresh]);
  async function perform(work: () => Promise<void>) {
    setError("");
    setNotice("");
    try {
      await work();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Request failed.");
    }
  }
  async function enrollPasskey() {
    const result = await api<{
      challengeId: string;
      options: PublicKeyCredentialCreationOptionsJSON;
    }>("/auth/register/options", post({ label: "Additional passkey" }));
    const response = await startRegistration({ optionsJSON: result.options });
    await api(
      "/auth/register/verify",
      post({ challengeId: result.challengeId, response }),
    );
    await refresh();
  }
  async function exportData() {
    const data = await api<unknown>("/export", post({}));
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(data, null, 2)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = "scratchpad-export.json";
    a.click();
    URL.revokeObjectURL(url);
    setNotice("Export downloaded.");
  }
  return (
    <section>
      <header className="page-heading">
        <div>
          <h1>Settings</h1>
          <p>Your memory. Your server. Your credentials.</p>
        </div>
      </header>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      <OwnerSettings />
      <AiSettings />
      <ProjectDefaults />
      <section className="settings-section">
        <h2>Passkeys and identities</h2>
        <p>Revoking a credential ends its sessions immediately.</p>
        <button type="button" onClick={() => void perform(enrollPasskey)}>
          Add passkey
        </button>
        <ul className="credential-list">
          {credentials.map((c) => (
            <li key={c.id}>
              <div>
                <strong>{c.label ?? c.kind}</strong>
                <small>
                  {c.kind}
                  {c.revokedAt ? " · Revoked" : ""}
                </small>
              </div>
              {!c.revokedAt && (
                <button
                  type="button"
                  onClick={() =>
                    void perform(async () => {
                      await api(`/auth/credentials/${c.id}`, {
                        method: "DELETE",
                      });
                      await refresh();
                    })
                  }
                >
                  Revoke
                </button>
              )}
            </li>
          ))}
        </ul>
      </section>
      <section className="settings-section">
        <h2>Connect an MCP key</h2>
        <p>
          Enroll a public SSH key and prove possession. Your private key stays
          on your machine.
        </p>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void perform(async () => {
              setChallenge(
                await api(
                  "/auth/credentials/challenge",
                  post({ publicKey, label: "MCP key" }),
                ),
              );
            });
          }}
        >
          <label>
            Public key
            <textarea
              value={publicKey}
              onChange={(e) => setPublicKey(e.target.value)}
              placeholder="ssh-ed25519 …"
              required
            />
          </label>
          <button type="submit">Create enrollment challenge</button>
        </form>
        {challenge && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void perform(async () => {
                await api(
                  "/auth/credentials/verify",
                  post({
                    challengeId: challenge.challengeId,
                    publicKey,
                    signature,
                  }),
                );
                setChallenge(null);
                setSignature("");
                setPublicKey("");
                await refresh();
                setNotice("MCP key enrolled.");
              });
            }}
          >
            <p>
              Sign this exact challenge using your key with namespace{" "}
              <code>{challenge.namespace}</code>.
            </p>
            <pre>{challenge.nonce}</pre>
            <label>
              Armored SSH signature
              <textarea
                value={signature}
                onChange={(e) => setSignature(e.target.value)}
                required
              />
            </label>
            <button type="submit" className="primary">
              Verify and enroll key
            </button>
          </form>
        )}
      </section>
      <section className="settings-section">
        <h2>Portable memory</h2>
        <p>
          Export records and audit history. Credentials stay on this instance.
        </p>
        <button type="button" onClick={() => void perform(exportData)}>
          Download export
        </button>
        <label className="import-label">
          Import Scratchpad export
          <input
            type="file"
            accept="application/json,.json"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file)
                void perform(async () => {
                  const data = JSON.parse(await file.text());
                  await importData(data);
                });
            }}
          />
        </label>
        <h3>Import a decision log</h3>
        <p>
          Choose a project and a JSONL file. Original entries are preserved;
          missing historical authority remains unknown.
        </p>
        <label>
          Import into project
          <select
            aria-label="Import into project"
            value={importProject}
            onChange={(e) => setImportProject(e.target.value)}
          >
            <option value="">Choose a project</option>
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Import JSONL decision log
          <input
            type="file"
            accept=".jsonl,application/x-ndjson,text/plain"
            disabled={!importProject}
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file)
                void perform(async () => {
                  await importData({
                    format: "jsonl",
                    projectId: importProject,
                    jsonl: await file.text(),
                    source: { filename: file.name },
                  });
                });
            }}
          />
        </label>
        {importWarnings.length > 0 && (
          <details>
            <summary>Import notes ({importWarnings.length})</summary>
            <ul>
              {importWarnings.map((warning) => (
                <li key={`${warning.record}-${warning.code}`}>
                  {warning.record ? `Line ${warning.record}: ` : ""}
                  {warning.code.replaceAll("_", " ").toLowerCase()}
                </li>
              ))}
            </ul>
          </details>
        )}
      </section>
    </section>
  );
}
