# Configure GitHub owner access

## Scope and prerequisites

Use a Scratchpad server built from the GitHub owner-access source revision or a
later release explicitly containing it. Published v0.3.0 predates this capability.
This runbook is operator guidance; it does not authorize changing a live instance.
Keep an independent credential and a tested operational backup before identity
changes. One instance has one owner across laptops and VMs.

## Configure the OAuth app

1. Confirm the instance's stable `SCRATCHPAD_PUBLIC_URL` origin. Use HTTPS outside
   loopback and keep the application behind its existing TLS/access controls.
2. Register a dedicated GitHub OAuth app following
   [GitHub's creation guide](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app).
   Set its homepage to the instance origin. Set its authorization callback URL to
   that exact origin followed by `/api/v1/auth/github/callback`; for example,
   `https://memory.example.com/api/v1/auth/github/callback`.
3. Put the app's client ID and secret into private server configuration as
   `SCRATCHPAD_GITHUB_CLIENT_ID` and `SCRATCHPAD_GITHUB_CLIENT_SECRET`. Set both
   together. Keep the secret out of source control, command transcripts and
   public artifacts. Supply these values through the deployment's existing
   secret mechanism rather than committing an example containing real values.
4. For the repository Compose deployment, the optional pair is passed through
   from the operator environment; leave both unset for passkey-only setup.
   Restart/recreate the server using its established deployment procedure. Verify
   readiness and that the setup/sign-in UI offers GitHub. The server derives the
   callback from its configured public origin; it is not an arbitrary redirect.
5. Use GitHub's standard browser authorization flow. Scratchpad needs account
   identity and public SSH keys, not private repository or key-write permissions.
   Do not enable device flow or configure a shared third-party login service.

The [GitHub browser flow reference](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#web-application-flow)
explains the authorization callback and code exchange. OAuth credentials belong
on the server only; the MCP does not use GitHub OAuth tokens or PATs.

## Establish the owner

For a fresh instance, generate the usual setup token with `npm run admin -- setup`
(or the container equivalent in the server runbook). Supply the single-use token
and choose GitHub on the setup page. Confirm the intended GitHub account before
authorizing. For an initialized instance, sign into the existing owner dashboard
and link through Settings. A descriptive username/profile is insufficient.

Verify the displayed account and synchronization status. Stable account ID is the
binding; usernames remain display/discovery metadata. Optionally add a passkey
for independent browser access. Existing passkeys and manually enrolled SSH keys
remain available. All client machines need network access to this instance.

## Connect a laptop or VM

Use the portable `skills/scratchpad-connect` folder with the client's supported
skill mechanism. Give the agent the server origin and intended checkout or project
ID. It discovers an existing public SSH authentication/signing key using its
preferred Git/SSH tools, configures the existing local stdio MCP and performs an
authenticated scoped read. Publishing a new GitHub key, generating an identity or
copying private material is not part of this workflow. An eligible synchronized
key needs no per-machine dashboard approval, but must prove possession locally.
Use manual enrollment for independent unpublished keys.

## Outages, blocks and identity changes

Inspect last successful synchronization, cache deadline and the last sync error
in Settings. Synchronization runs every five minutes and applies only after both
paginated categories are complete. A failed/malformed refresh keeps prior state;
restart preserves its age. A manual refresh can restore freshness once GitHub is
available. GitHub-managed requests and session renewal stop after 24 hours without
success, even if a token's own expiry is later. Independent local credentials
remain available. Browser session expiry requires GitHub or independent sign-in.

For a stolen key, block it locally immediately and remove it on GitHub. Removal
is detected on successful synchronization; an outage can delay that detection up
to the cache limit. The local block survives removal and re-addition. Only an
explicit unblock restores eligibility.

Before replacement, perform fresh authentication with an existing credential.
Before unlinking, authenticate with an independent local credential. The freshness
window is five minutes from proof, not renewal. Add a passkey first if needed.
Use `npm run admin -- recover` if all access is lost. Authorized recovery can
establish independent passkey access or replace the GitHub account. Unlinking and
replacement preserve knowledge/local credentials and invalidate GitHub-derived
access. They never convert synchronized credentials into independent keys.

## Verify in a disposable environment

Use synthetic accounts, generated disposable SSH keys and isolated databases.
The existing browser → HTTP → MCP acceptance harness controls the GitHub service
and clock. It must prove GitHub-first token-authorized setup, wrong-account denial,
a scoped authenticated read, renewal during outage, the 24-hour request gate,
complete-set removal, persistent blocking, independent access and recovery.
Focused HTTP scenarios cover OAuth replay/browser binding and malformed/paginated
refresh failures. Repeat restart behavior for SQLite and PostgreSQL as supported
by the harness. Never send synthetic keys to real GitHub or write test captures
to the owner's live instance.

Run docs validation from the repository with its pinned Node runtime:

```sh
npm run check -w @scratchpad/docs
npm run build -w @scratchpad/docs
npm run lint
```

Inspect the generated agent Markdown and links. Static build and synthetic
workflow success do not prove installation into a user's client, OAuth app
configuration in production or deployed acceptance. GitHub Actions is canonical.
