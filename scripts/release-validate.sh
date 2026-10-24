#!/usr/bin/env bash
# Runtime validation of the archive built for this host's OS and architecture:
# the executable must run and report the release version. Extraction happens in
# TMPDIR so RELEASE_OUT keeps only the declared archive.
set -euo pipefail

: "${RELEASE_VERSION:?Set RELEASE_VERSION}"
: "${RELEASE_OS:?Set RELEASE_OS}"
: "${RELEASE_ARCH:?Set RELEASE_ARCH}"
: "${RELEASE_OUT:?Set RELEASE_OUT}"

name="scratchpad-mcp-v${RELEASE_VERSION}-${RELEASE_OS}-${RELEASE_ARCH}"
work=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-mcp-validate.XXXXXX")
trap 'rm -rf "$work"' EXIT
if [[ "$RELEASE_OS" == darwin ]]; then
  archive="$RELEASE_OUT/$name.zip"
  [[ $(unzip -Z1 "$archive") == scratchpad-mcp ]]
  unzip -q "$archive" scratchpad-mcp -d "$work"
else
  archive="$RELEASE_OUT/$name.tar.gz"
  [[ $(tar -tzf "$archive") == scratchpad-mcp ]]
  tar -xzf "$archive" -C "$work" scratchpad-mcp
fi
chmod 755 "$work/scratchpad-mcp"
[[ $("$work/scratchpad-mcp" --version) == "scratchpad-mcp ${RELEASE_VERSION}" ]]
printf 'Validated %s\n' "$name"
