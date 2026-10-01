#!/usr/bin/env bash
set -euo pipefail

: "${RELEASE_TAG:?Missing release tag}"
: "${EXPECTED_SHA:?Missing source commit}"
: "${APPLE_TEAM_ID:?Missing trusted public team ID}"
: "${TARGET_ARCH:?Missing architecture}"
: "${ASSET_DIR:?Missing downloaded asset directory}"
[[ $(uname -s) == Darwin ]]
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$TARGET_ARCH" == arm64 || "$TARGET_ARCH" == amd64 ]]
root=$(git rev-parse --show-toplevel)
archive="$ASSET_DIR/scratchpad-mcp-$RELEASE_TAG-darwin-$TARGET_ARCH.zip"
receipt="$ASSET_DIR/notarization-darwin-$TARGET_ARCH.json"
manifest="$ASSET_DIR/macos-release.json"
jq -e --arg tag "$RELEASE_TAG" --arg sha "$EXPECTED_SHA" --arg team "$APPLE_TEAM_ID" \
  '.tag == $tag and .sourceSha == $sha and .teamId == $team' "$manifest" >/dev/null
[[ $(shasum -a 256 "$archive" | awk '{print $1}') == $(jq -er --arg arch "$TARGET_ARCH" '.artifacts[$arch].sha256' "$manifest") ]]
[[ $(shasum -a 256 "$receipt" | awk '{print $1}') == $(jq -er --arg arch "$TARGET_ARCH" '.artifacts[$arch].receiptSha256' "$manifest") ]]
jq -e '.status == "Accepted" and (.id | type == "string")' "$receipt" >/dev/null
# Restrict extraction to one expected binary; no paths or links from the archive.
[[ $(unzip -Z1 "$archive") == scratchpad-mcp ]]
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
unzip -p "$archive" scratchpad-mcp > "$work/scratchpad-mcp"
chmod 755 "$work/scratchpad-mcp"
codesign --verify --strict --verbose=2 "$work/scratchpad-mcp"
codesign -dv --verbose=4 "$work/scratchpad-mcp" 2> "$work/signature.txt"
grep -Fq 'Authority=Developer ID Application:' "$work/signature.txt"
grep -Fxq "TeamIdentifier=$APPLE_TEAM_ID" "$work/signature.txt"
grep -Fq 'runtime' "$work/signature.txt"
# Ask Apple for the executable's notarization ticket, independently of its receipt.
# Trigger online ticket retrieval. spctl rejects bare CLIs as "not an app",
# so its exit status is not the gate; the explicit requirement below is mandatory.
spctl --assess --type execute "$work/scratchpad-mcp" > "$work/assessment.txt" 2>&1 || true
codesign --verify --strict --verbose=2 --test-requirement '=notarized' "$work/scratchpad-mcp"
# Normalize signatures on disposable copies before comparing actual executable bytes.
cp "$root/dist/release/darwin-$TARGET_ARCH/scratchpad-mcp" "$work/reference"
for file in "$work/scratchpad-mcp" "$work/reference"; do
  if codesign -d "$file" >/dev/null 2>&1; then codesign --remove-signature "$file"; fi
  codesign --force --sign - --identifier scratchpad-mcp --options 0 "$file"
done
cmp "$work/reference" "$work/scratchpad-mcp"
printf 'Verified locally signed %s archive against source %s.\n' "$TARGET_ARCH" "$EXPECTED_SHA"
