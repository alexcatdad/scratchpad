# MCP acceptance runbook

This runbook maps the local MCP boundary to PRD sections 37–38. All automated fixtures use temporary repositories, keys, files, and HTTP servers. They never import a real repository, modify a contractor checkout, or use production credentials.

## Repeatable checks

From `mcp/`:

```sh
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

Run the focused product scenarios:

```sh
go test -race ./internal/scratchpad -run 'TestAcceptance|TestReleaseVersion' -count=1 -v
```

Verify release version injection without changing the checkout:

```sh
go build -ldflags '-X main.version=0.1.0-test' -o /tmp/scratchpad-mcp-version-check ./cmd/scratchpad-mcp
/tmp/scratchpad-mcp-version-check --version
```

An ordinary local build reports `dev`; release tooling supplies the tag version. The MCP initialization response uses the same injected version.

## Coverage and evidence boundaries

| PRD requirement                                  | Evidence                                                                                              | What the check proves                                                                                                                                                                                |
| ------------------------------------------------ | ----------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Eight typed capture tools                        | `TestProtocolToolsAndCapture`, actual production browser workflow                                     | All eight schemas reach HTTP with natural payloads, metadata, and stable request IDs; the production workflow checks persisted readable content                                                      |
| Typed stdio protocol                             | `TestStdioProtocol`                                                                                   | A real subprocess initializes, lists 14 tools, and rejects incomplete typed input                                                                                                                    |
| SSH challenge authentication                     | `TestSSHChallengeAuthentication`, production browser workflow                                         | OpenSSH verifies the exact nonce/signature; browser enrollment and fresh MCP authentication use the production API in the full workflow                                                              |
| Ambiguous identity                               | `TestAcceptanceAmbiguityExplicitResolutionPreservesProvenance`                                        | Competing non-origin remotes produce an explicit error before any API capture; owner-confirmed resolution permits capture without inventing a remote and retains checkout provenance                 |
| Non-Git project, scenario F                      | `TestAcceptanceNonGitExplicitResolutionAndRetry`, production integration projects phase               | Weak local identity is rejected, owner-confirmed name resolves, explicit project capture/retrieval/retry succeeds, and no Git context is invented                                                    |
| Worktrees, scenario E                            | `TestAcceptanceWorktreesShareProjectAndKeepIndependentContext`, production integration projects phase | Two real Git worktrees resolve through the same repository identity while preserving distinct branch/commit/worktree context; one override does not change subsequent default-directory calls        |
| Contractor repository, scenario D                | `TestAcceptanceContractorRepositoryUntouched`, production integration projects phase                  | Central capture succeeds with local mirroring off, and every working-tree/Git-metadata file retains its content; no mirror/status write occurs                                                       |
| Default decision mirroring                       | `TestAcceptanceMirrorDefaultTypesRetryAndCentralFailure`                                              | Given server permission for the three default decision types, only those are appended; all eight central captures remain available, retries append no duplicates, failed central writes never mirror |
| Both mirror permission gates and partial success | `TestCaptureMirrorGatesAndPartialSuccess`                                                             | Either disabled gate prevents local writes; mirror failure preserves central success; retry does not duplicate local records                                                                         |
| Concurrent/local mirror safety                   | `TestMirrorConcurrentRetry`, `TestMirrorRejectsSymlinkEscapeBeforeCreatingDirectories`                | Concurrent retry appends once, and traversal/symlink escapes cannot create files or directories outside the checkout                                                                                 |
| Server restart and persisted retries, scenario G | `tests/workflow.spec.ts` with `mcp/integration`                                                       | Real browser bootstrap and key enrollment, capture through the built stdio executable, production-server restart, fresh MCP session, existing-record replay, changed-payload conflict                |
| Search and relationship/history inspection       | `mcp/integration`                                                                                     | The real server returns all captured records in project search and answers context, decision history, record inspection, and related-record requests                                                 |

The Go acceptance HTTP fixture observes the local integration boundary; it does not reimplement or prove SQLite persistence, server project policy, passkey registration, import fidelity, or history semantics. Those belong to server tests and the production integration workflow. In particular, default mirror types originate in server settings; MCP trusts current `mirror.eligible` rather than duplicating policy locally.

## Run the real browser-to-MCP workflow

From the repository root, after dependency installation:

```sh
npm run build
npx playwright install chromium
npm run test:e2e
```

See [the integration runbook](../../mcp/integration/README.md) for manually running capture and verification against a disposable production server. The automated browser test creates its own disposable SQLite database, virtual authenticator, SSH key, and Git workspace.

## Acceptance decisions and limits

- Selecting a project explicitly resolves identity ambiguity, but does not manufacture an identity for one competing remote. Branch, commit, dirty state, checkout root, and worktree provenance remain available.
- Git inspection runs with `--no-optional-locks`, preventing optional index-refresh writes during contractor-repository discovery.
- Local mirrors require local configuration and current server eligibility. The server must recompute eligibility on idempotent replay so disabling project mirroring also disables later retries; the original immutable record remains unchanged.
- MCP never commits or pushes a mirror. Central success and local mirror/report failures remain distinct.
- Scenarios A–C require real imported historical records and server-side interpretation of amendments/replacements. Passing MCP transport tests alone does not prove those scenarios.
- Release publication, notarization, GHCR availability, Homebrew installation, and the actual production deployment are separate acceptance evidence; a local MCP build does not establish them.

## Validate an installed release without using an existing server

The browser workflow accepts `SCRATCHPAD_E2E_MCP_BINARY`, a filesystem path to an already installed or downloaded MCP executable. When supplied, the test skips building the MCP server and passes that exact executable to both the capture phase and the fresh-process retrieval/retry phase. It prints the selected path and `--version` result. Go is still needed to run the SDK-based test client; the test client does not replace the selected MCP executable.

The server, passkey, SSH key, project checkout, records, and browser session are all disposable fixtures. The test does not change an installed MCP client's configuration or connect to the owner's existing Scratchpad instance. Cleanup removes only test-created files and, in Docker mode, test-created containers and volumes; it never removes or edits the supplied executable.

After release publication and an explicitly authorized Homebrew installation:

```sh
brew info --json=v2 alexcatdad/tap/scratchpad-mcp
brew list --versions scratchpad-mcp
brew test alexcatdad/tap/scratchpad-mcp
"$(brew --prefix scratchpad-mcp)/bin/scratchpad-mcp" --version
npm run build -w @scratchpad/web
SCRATCHPAD_E2E_MCP_BINARY="$(brew --prefix scratchpad-mcp)/bin/scratchpad-mcp" npm run test:e2e
```

Check the formula version and executable output against the intended release version before claiming acceptance. `brew test` checks the formula's own smoke test; the final command additionally proves real SSH enrollment, capture and retrieval through the installed executable, server restart, and persisted retry identity. Run on each platform whose installation is being accepted; one Apple Silicon installation does not prove Intel macOS or Linux installation.

To also validate the installed MCP against a published application image, pull the exact release image first, then select it explicitly:

```sh
SCRATCHPAD_E2E_DOCKER=1 SCRATCHPAD_E2E_IMAGE=ghcr.io/alexcatdad/scratchpad:v0.1.2 SCRATCHPAD_E2E_MCP_BINARY="$(brew --prefix scratchpad-mcp)/bin/scratchpad-mcp" npm run test:e2e
```

This adds the named-volume backup/restore workflow documented in [container acceptance](container-acceptance.md). Pulling an image, installing or upgrading a formula, verifying published checksums/signatures, and checking Apple notarization are separate release steps described in [the release runbook](release.md). The runner does not install, upgrade, publish, sign, or notarize anything.

## Production API project scenarios

The browser workflow now also invokes `mcp/integration -phase projects` against the actual authenticated HTTP API and selected stdio executable. It provisions a disposable Git-identified external project through the owner's browser session, then verifies:

- Local mirroring is enabled, but server policy denies it; central capture succeeds and every checkout/Git metadata file remains byte-for-byte unchanged, including on retry.
- The main checkout and a real linked worktree share one project, retain distinct branch/commit/root/worktree provenance, and return to the default launch directory after a per-call override.
- A non-Git folder produces `PROJECT_IDENTITY_REQUIRED`; an owner-confirmed name resolves it and allows central-only capture/retry without invented Git provenance or folder writes.
- All five additional captures, their identities and their original Git context survive the same restart or full backup/restore as the original eight typed captures.

The fixtures, worktree and extra project data are disposable. This combined workflow supplements the focused Go boundary tests; it does not use the owner's real contractor repository or change client configuration.
