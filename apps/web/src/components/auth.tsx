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
import { api, post } from "../lib/api";
export function AuthScreen({
  initialized,
  onAuthenticated,
}: {
  initialized: boolean;
  onAuthenticated: () => void;
}) {
  const [token, setToken] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [error, setError] = useState("");
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
        const result = await api<{
          challengeId: string;
          options: PublicKeyCredentialRequestOptionsJSON;
        }>("/auth/login/options", post({}));
        response = await startAuthentication({ optionsJSON: result.options });
        await api(
          "/auth/login/verify",
          post({ challengeId: result.challengeId, response }),
        );
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
  return (
    <main className="auth-page">
      <div className="auth-box">
        <a className="wordmark" href="/">
          Scratchpad
        </a>
        <h1>{enroll ? "Make room for your memory." : "Welcome back."}</h1>
        <p>
          {enroll
            ? "Connect a passkey to your private Scratchpad. Your project history stays on your server."
            : "Sign in with your passkey to pick up where you left off."}
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
          <button type="submit" className="primary" disabled={busy}>
            {busy
              ? "Waiting for your passkey…"
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
