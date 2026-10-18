#!/usr/bin/env bash
set -euo pipefail
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
go -C "$repo_root/mcp" test -count=1 -timeout=3m -run '^TestHomebrewUpgradeAcceptanceHandoffPreservesQueueAndLifecycle$' ./cmd/scratchpad-service
