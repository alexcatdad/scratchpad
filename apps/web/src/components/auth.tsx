import type {
  AuthenticationResponseJSON,
  PublicKeyCredentialCreationOptionsJSON,
  PublicKeyCredentialRequestOptionsJSON,
  RegistrationResponseJSON,
} from "@simplewebauthn/browser";
import {
  startAuthentication,
  startRegistration,
} from "@simplewebauthn/browser";
import { useState } from "react";
import { api, post, signInWithGithub } from "../lib/api";
export async function authenticatePasskey(): Promise<void> {
  const result = await api<{
    challengeId: string;
    options: PublicKeyCredentialRequestOptionsJSON;
  }>("/auth/login/options", post({}));
  const response = await startAuthentication({ optionsJSON: result.options });
  await api(
    "/auth/login/verify",
    post({ challengeId: result.challengeId, response }),
  );
}
export function AuthScreen({
  initialized,
  githubConfigured,
  initialError,
  onAuthenticated,
}: {
  initialized: boolean;
  githubConfigured: boolean;
  initialError?: string;
  onAuthenticated: () => void;
}) {
  const [token, setToken] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [error, setError] = useState(initialError ?? "");
  const [busy, setBusy] = useState(false);
  const enroll = !initialized || recovery;
  async function authenticate() {
    setError("");
    setBusy(true);
    try {
      let response: RegistrationResponseJSON | AuthenticationResponseJSON;
      if (enroll) {
        const result = await api<{
          challengeId: string;
          options: PublicKeyCredentialCreationOptionsJSON;
        }>(
          "/auth/register/options",
          post({ setupToken: token, label: "Browser passkey" }),
        );
        response = await startRegistration({ optionsJSON: result.options });
        await api(
          "/auth/register/verify",
          post({
            challengeId: result.challengeId,
            setupToken: token,
            response,
          }),
        );
      } else {
        await authenticatePasskey();
      }
      onAuthenticated();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Authentication failed.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function githubSignIn() {
    setBusy(true);
    setError("");
    try {
      await signInWithGithub(
        recovery ? "recover" : initialized ? "login" : "setup",
        enroll ? token : undefined,
      );
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "GitHub sign-in failed.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="auth-page">
      <div className="auth-box">
        <a className="wordmark" href="/">
          Scratchpad
        </a>
        <h1>{enroll ? "Make room for your memory." : "Welcome back."}</h1>
        <p>
          {enroll
            ? "Use your administrator token to set up access. Your project history stays on your server."
            : "Sign in to pick up where you left off."}
        </p>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void authenticate();
          }}
        >
          {enroll && (
            <label>
              Setup or recovery token
              <input
                value={token}
                onChange={(event) => setToken(event.target.value)}
                type="password"
                required
                autoComplete="off"
              />
              <small>
                Generate a single-use token with the administrator command on
                your server.
              </small>
            </label>
          )}
          {error && (
            <p role="alert" className="error">
              {error}
            </p>
          )}
          {githubConfigured && (
            <>
              <button
                type="button"
                className="primary"
                disabled={busy || (enroll && !token.trim())}
                onClick={() => void githubSignIn()}
              >
                {recovery ? "Recover with GitHub" : "Sign in with GitHub"}
              </button>
              {recovery && (
                <p>
                  Recovery replaces the linked GitHub account and ends its
                  existing access. Project data and independent credentials are
                  preserved.
                </p>
              )}
              {enroll && <p>Passkeys are optional when using GitHub.</p>}
            </>
          )}
          <button type="submit" className="primary" disabled={busy}>
            {busy
              ? "Authenticating…"
              : enroll
                ? "Register passkey"
                : "Sign in with passkey"}
          </button>
        </form>
        {initialized && (
          <button
            className="text-button"
            type="button"
            onClick={() => {
              setRecovery(!recovery);
              setError("");
            }}
          >
            {recovery ? "Back to sign in" : "Recover access"}
          </button>
        )}
        <a className="quiet" href="https://alexcatdad.github.io/scratchpad/">
          Setup documentation
        </a>
      </div>
    </main>
  );
}
