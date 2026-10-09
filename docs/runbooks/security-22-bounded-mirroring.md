# Bounded optional repository mirroring

The central API saves the immutable capture first. Optional mirroring still requires both local enablement and project permission. A mirror failure returns the saved record identity and an explicit partial-success result; retry with the same `requestId` preserves that identity and avoids duplicate local appends.

Mirror targets must be regular files before and after opening. Root-scoped opens use nonblocking and no-follow flags so replacing a previously inspected file with a FIFO or symlink cannot trap the opener. Opened identity is compared with the current target before scanning.

The capture's existing five-second mirror context governs lock acquisition, bounded 32 KiB scan reads, JSONL processing and append boundaries. Cancellation releases owned locks; no detached scanner continues after return. The 16 MiB maximum line length remains unchanged. Filesystem metadata calls, individual kernel I/O and sync require a healthy local filesystem; context checks cannot preempt an unresponsive filesystem syscall.

1. Keep mirroring disabled for untrusted or unapproved repositories. Inspect the configured target before enabling it; never point it at a device, FIFO or symlink.
2. If a capture reports mirror failure, retain its central record identity. Repair only the selected mirror path. A crashed writer's stale lock still requires explicit operator inspection/recovery; cancellation of a live writer releases its own lock automatically.
3. Retry with the original `requestId`; verify the central identity is unchanged and the local regular-file mirror contains one record.

Run synthetic regression and normal retry/protocol checks:

```sh
cd mcp
go test -race ./...
go vet ./...
go build ./cmd/scratchpad-mcp
```

Tests bound special-file attempts in subprocesses, replace a regular target with a FIFO while its lock is held, exercise cancellation during a large regular-file scan, and verify the actual MCP partial-success/retry result. No owner's captures or deployed compromise evidence are used.
