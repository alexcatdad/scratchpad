import { useCallback, useEffect, useState } from "react";
import { api, post, signInWithGithub } from "../lib/api";
import { authenticatePasskey } from "./auth";

type GithubStatus = {
  configured: boolean;
  binding: { accountId: string; username: string } | null;
  lastSuccessfulSyncAt: string | null;
  cacheExpiresAt: string | null;
  cacheValid: boolean;
  lastSyncError: string | null;
  freshAuthenticationRequired: boolean;
  keys: {
    publicKey: string;
    fingerprint: string;
    categories: string[];
    blocked: boolean;
  }[];
};
const date = (value: string) => new Date(value).toLocaleString("en");

export function GithubAccess({
  onChanged,
}: {
  onChanged: () => Promise<void>;
}) {
  const [status, setStatus] = useState<GithubStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const refresh = useCallback(async () => {
    setStatus(await api<GithubStatus>("/auth/github"));
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
        reason instanceof Error ? reason.message : "GitHub request failed.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="settings-section" aria-label="GitHub access">
      <h2>GitHub access</h2>
      <p>
        Verified GitHub sign-in and synchronized machine keys belong to the same
        owner. Public profile linkage is separate and grants no access.
      </p>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {!status ? (
        <button
          type="button"
          disabled={busy}
          onClick={() => void perform(refresh)}
        >
          Retry loading GitHub access
        </button>
      ) : !status.configured ? (
        <p>
          The administrator has not configured GitHub sign-in. Passkeys and
          independent machine keys remain available.
        </p>
      ) : !status.binding ? (
        <button
          type="button"
          disabled={busy}
          onClick={() => void perform(() => signInWithGithub("link"))}
        >
          Link GitHub account
        </button>
      ) : (
        <>
          <p>
            Signed-in account: <strong>@{status.binding.username}</strong> ·
            Account ID: {status.binding.accountId}
          </p>
          <p>
            {status.lastSuccessfulSyncAt
              ? `Last successful synchronization: ${date(status.lastSuccessfulSyncAt)}.`
              : "No successful key synchronization yet."}
          </p>
          <p>
            {status.cacheValid && status.cacheExpiresAt
              ? `Cached machine access is available until ${date(status.cacheExpiresAt)} if GitHub is unavailable.`
              : "GitHub-managed machine access is unavailable until keys synchronize successfully."}
          </p>
          <p>
            Keys synchronize every five minutes. Machines must prove possession.
            Removed keys lose access after successful synchronization; local
            blocks take effect immediately and persist until you unblock them.
          </p>
          {status.lastSyncError && (
            <p role="alert" className="error">
              {status.lastSyncError}
            </p>
          )}
          <button
            type="button"
            disabled={busy}
            onClick={() =>
              void perform(async () => {
                try {
                  setStatus(
                    await api<GithubStatus>("/auth/github/sync", post({})),
                  );
                  setNotice("GitHub keys synchronized.");
                } catch (reason) {
                  await refresh();
                  throw reason;
                }
                await onChanged();
              })
            }
          >
            Synchronize keys
          </button>
          <ul className="credential-list">
            {status.keys.map((key) => (
              <li key={key.fingerprint}>
                <div>
                  <strong>{key.fingerprint}</strong>
                  <small>
                    GitHub-managed · {key.categories.join(", ")} ·{" "}
                    {key.blocked
                      ? "Blocked locally"
                      : "Eligible after possession proof"}
                  </small>
                </div>
                <button
                  type="button"
                  disabled={busy}
                  aria-label={`${key.blocked ? "Unblock key" : "Block key"} ${key.fingerprint}`}
                  onClick={() =>
                    void perform(async () => {
                      setStatus(
                        await api<GithubStatus>(
                          "/auth/github/block",
                          post({
                            publicKey: key.publicKey,
                            blocked: !key.blocked,
                          }),
                        ),
                      );
                      await onChanged();
                    })
                  }
                >
                  {key.blocked ? "Unblock key" : "Block key"}
                </button>
              </li>
            ))}
          </ul>
          {!status.keys.length && (
            <p>
              No published SSH keys are available. You can enroll an independent
              key below.
            </p>
          )}
          <h3>Change account access</h3>
          <p>
            Replacing or disconnecting GitHub ends GitHub-derived sessions and
            machine permissions. Project data and independent credentials are
            preserved.
          </p>
          <p>
            Replacement requires sign-in within the last five minutes.
            Disconnecting always asks for an independent passkey. If you have
            none, add one below or use administrator recovery from the sign-in
            screen.
          </p>
          <button
            type="button"
            disabled={busy}
            onClick={() =>
              void perform(async () => {
                await authenticatePasskey();
                await refresh();
                setNotice("Fresh passkey authentication verified.");
              })
            }
          >
            Sign in again with passkey
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => void perform(() => signInWithGithub("login"))}
          >
            Sign in again with GitHub
          </button>
          {status.freshAuthenticationRequired && (
            <p>Sign in again before replacing the account.</p>
          )}
          <button
            type="button"
            disabled={busy}
            onClick={() => void perform(() => signInWithGithub("replace"))}
          >
            Replace GitHub account
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() =>
              void perform(async () => {
                await authenticatePasskey();
                await api("/auth/github", { method: "DELETE" });
                await refresh();
                await onChanged();
                setNotice(
                  "GitHub disconnected. Independent credentials remain available.",
                );
              })
            }
          >
            Disconnect GitHub
          </button>
        </>
      )}
    </section>
  );
}
