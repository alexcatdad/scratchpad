# Non-executing checkout discovery verification

Issue #15 is independently based on main. Use a trusted installed Git 2.36 or later and Go 1.27.2.

1. From mcp, run go test -race ./..., go vet ./... and go build ./cmd/scratchpad-mcp. Remove the resulting local binary after checking it; never commit it.
2. From the repository root, run npm run lint, npm run typecheck and npm run build with Node 24.21.0.
3. Run the focused TestScopedToolsDoNotExecuteCheckoutHelpers and TestDiscoveryGitVersionBoundary tests. They use disposable repositories, local submodules and harmless marker hooks, with positive controls proving the hooks are executable. Real MCP context, search, capture, history and scoped AI calls must not create the markers. Worktree and explicit project-ID capture provenance remain correct.

Every Git provenance invocation overrides core.fsmonitor=false, submodule.recurse=false and status.submoduleSummary=false. Status additionally uses --ignore-submodules=all. Parent tracked/untracked changes still mark the checkout dirty; nested submodule changes do not. The trusted Git executable and process environment remain operator-owned.

Git's [configuration manual](https://git-scm.com/docs/git-config#Documentation/git-config.txt-corefsmonitor) documents that 2.35.1 and older interpret boolean fsmonitor settings as executable paths. Runtime version admission requires at least 2.36 before any checkout inspection. Compatibility tests reject 2.35.1 and unparseable versions, accept the minimum 2.36 and a vendor-suffixed current version, and exercise the actual installed Git for helper suppression. The [status manual](https://git-scm.com/docs/git-status#Documentation/git-status.txt---ignore-submoduleswhen) defines explicit suppression of submodule inspection. No live repositories, credentials or deployed state are modified.
