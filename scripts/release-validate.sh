#!/usr/bin/env bash
# Runtime validation of the app archive on an Apple Silicon host: the archive
# holds exactly Scratchpad.app, its signature verifies and the bundled helpers
# report the release version. Extraction happens in TMPDIR so RELEASE_OUT keeps
# only the declared archive.
set -euo pipefail

: "${RELEASE_VERSION:?Set RELEASE_VERSION}"
: "${RELEASE_OUT:?Set RELEASE_OUT}"

archive="$RELEASE_OUT/scratchpad-${RELEASE_VERSION}-darwin-arm64.zip"
work=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-validate.XXXXXX")
trap 'rm -rf "$work"' EXIT
[[ -z $(unzip -Z1 "$archive" | grep -v '^Scratchpad\.app/') ]]
ditto -x -k "$archive" "$work"
app="$work/Scratchpad.app"
codesign --verify --strict --deep "$app"
[[ $(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$app/Contents/Info.plist") == "$RELEASE_VERSION" ]]
[[ $("$app/Contents/Helpers/scratchpad-mcp" --version) == "scratchpad-mcp ${RELEASE_VERSION}" ]]
"$app/Contents/Helpers/scratchpad-service" --version | grep -q "^scratchpad-service ${RELEASE_VERSION} "
printf 'Validated Scratchpad.app %s\n' "$RELEASE_VERSION"
