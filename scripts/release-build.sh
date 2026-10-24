#!/usr/bin/env bash
# Build one scratchpad-mcp release archive. Called by the Taskfile build tasks
# with the environment reltool provides; it never reads CI or Git-ref state.
#
#   RELEASE_VERSION  version being released (YYYY.M.N)
#   RELEASE_OS       darwin or linux
#   RELEASE_ARCH     arm64 or amd64
#   RELEASE_OUT      directory that receives exactly the declared archive
#   MACOS_SIGNING_IDENTITY  optional; set by reltool only for signed targets
#   MACOS_ENTITLEMENTS      optional; unused (the CLI needs no entitlements)
set -euo pipefail

: "${RELEASE_VERSION:?Set RELEASE_VERSION}"
: "${RELEASE_OS:?Set RELEASE_OS to darwin or linux}"
: "${RELEASE_ARCH:?Set RELEASE_ARCH to arm64 or amd64}"
: "${RELEASE_OUT:?Set RELEASE_OUT to the output directory}"
[[ "$RELEASE_OS" == darwin || "$RELEASE_OS" == linux ]]
[[ "$RELEASE_ARCH" == arm64 || "$RELEASE_ARCH" == amd64 ]]

root=$(cd "$(dirname "$0")/.." && pwd)
stage=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-mcp-build.XXXXXX")
trap 'rm -rf "$stage"' EXIT
mkdir -p "$RELEASE_OUT"
tag="v${RELEASE_VERSION}"
name="scratchpad-mcp-${tag}-${RELEASE_OS}-${RELEASE_ARCH}"

(
  cd "$root/mcp"
  GOTOOLCHAIN=$(awk '/^go / {print "go" $2}' go.mod)
  export GOTOOLCHAIN
  CGO_ENABLED=0 GOOS="$RELEASE_OS" GOARCH="$RELEASE_ARCH" go build \
    -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${RELEASE_VERSION}" \
    -o "$stage/scratchpad-mcp" ./cmd/scratchpad-mcp
)

if [[ "$RELEASE_OS" == darwin ]]; then
  if [[ -n "${MACOS_SIGNING_IDENTITY:-}" ]]; then
    codesign --force --timestamp --options runtime \
      --sign "$MACOS_SIGNING_IDENTITY" "$stage/scratchpad-mcp"
    codesign --verify --strict "$stage/scratchpad-mcp"
  fi
  # The standalone executable sits at the archive root, without resource sidecars.
  /usr/bin/zip -q -j -X "$RELEASE_OUT/$name.zip" "$stage/scratchpad-mcp"
else
  tar -czf "$RELEASE_OUT/$name.tar.gz" -C "$stage" scratchpad-mcp
fi
