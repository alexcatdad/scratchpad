#!/bin/sh
# Backend race tests with a statement-coverage floor. The suite runs
# scratchpad-server as a child process, so the server is built with -cover and
# its coverage is collected from GOCOVERDIR rather than from the test binary.
# Usage: coverage-backend.sh <floor-percent> [go test flags...]
set -eu

floor=$1
shift

root=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$root/coverage"
data="$root/coverage/go-backend-data"
profile="$root/coverage/go-backend.out"
rm -rf "$data"
mkdir -p "$data"

SCRATCHPAD_SERVER_COVERDIR="$data" go -C "$root/backend" test -race -count=1 "$@" ./...
go tool covdata textfmt -i="$data" -o="$profile"
total=$(go -C "$root/backend" tool cover -func="$profile" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')
if [ -z "$total" ]; then
	printf '%s\n' 'Could not read total backend coverage.' >&2
	exit 1
fi
if ! awk -v total="$total" -v floor="$floor" 'BEGIN { exit !(total >= floor) }'; then
	printf 'Go coverage for backend is %s%%, below the %s%% floor.\n' "$total" "$floor" >&2
	exit 1
fi
printf 'Go coverage for backend: %s%% (%s%% floor)\n' "$total" "$floor"
