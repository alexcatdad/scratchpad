#!/bin/sh
# Go race tests with a statement-coverage floor for one module.
# Usage: coverage-go.sh <module-dir> <floor-percent> <coverpkg> [go test flags...]
set -eu

module=$1
floor=$2
coverpkg=$3
shift 3

root=$(cd "$(dirname "$0")/.." && pwd)
name=$(printf '%s' "$module" | tr '/' '-')
mkdir -p "$root/coverage"
profile="$root/coverage/go-$name.out"

go -C "$root/$module" test -race -count=1 -covermode=atomic -coverpkg="$coverpkg" \
	-coverprofile="$profile" "$@" ./...
total=$(go -C "$root/$module" tool cover -func="$profile" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')
if [ -z "$total" ]; then
	printf 'Could not read total Go coverage for %s.\n' "$module" >&2
	exit 1
fi
if ! awk -v total="$total" -v floor="$floor" 'BEGIN { exit !(total >= floor) }'; then
	printf 'Go coverage for %s is %s%%, below the %s%% floor.\n' "$module" "$total" "$floor" >&2
	exit 1
fi
printf 'Go coverage for %s: %s%% (%s%% floor)\n' "$module" "$total" "$floor"
