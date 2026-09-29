#!/usr/bin/env bash
set -euo pipefail

: "${RELEASE_TAG:?Set RELEASE_TAG to the existing stable release tag}"
: "${TARGET_OS:?Set TARGET_OS to darwin or linux}"
: "${TARGET_ARCH:?Set TARGET_ARCH to arm64 or amd64}"
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$TARGET_OS" == darwin || "$TARGET_OS" == linux ]]
[[ "$TARGET_ARCH" == arm64 || "$TARGET_ARCH" == amd64 ]]
root=$(git rev-parse --show-toplevel)
output="$root/dist/release/${TARGET_OS}-${TARGET_ARCH}"
mkdir -p "$output"
(
  cd "$root/mcp"
  CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" go build \
    -trimpath -ldflags="-s -w -X main.version=${RELEASE_TAG#v}" \
    -o "$output/scratchpad-mcp" ./cmd/scratchpad-mcp
)
if [[ "$TARGET_OS" == linux ]]; then
  tar -czf "$root/dist/release/scratchpad-mcp-${RELEASE_TAG}-linux-${TARGET_ARCH}.tar.gz" \
    -C "$output" scratchpad-mcp
fi
