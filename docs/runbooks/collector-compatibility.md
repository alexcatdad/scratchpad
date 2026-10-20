# Collector connection compatibility

Capture delivery, periodic connection probes and explicit connection verification
require the authenticated server identity to advertise `clientSavedAt-v1`.
A server without that capability must stay `server_incompatible`, even after a
successful authentication probe; its queued capture remains blocked.

From the repository root, run the deterministic status regression and the
original asynchronous delivery scenario:

```sh
go -C mcp test -race -count=20 -run '^(TestConnectionChecksPreserveCaptureCompatibility|TestUncertainDeliveryFreezesProjectSelectionAndLegacyServerIsBlocked)$' ./internal/localcapture
go -C mcp test -race -count=1 ./...
go -C mcp vet ./...
```

The deterministic regression rejects a legacy server during delivery, then
refreshes status through each connection-check path. It also checks that a
compatible server remains connected. All stores, keys and captures are synthetic.
