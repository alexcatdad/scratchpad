#!/usr/bin/env bash
set -euo pipefail

# Run locally. Private keys and notarization credentials stay in the Keychain.
: "${RELEASE_TAG:?Set the existing stable release tag}"
: "${APPLE_SIGNING_IDENTITY:?Set the local Developer ID Application identity}"
: "${APPLE_TEAM_ID:?Set the expected public Apple team ID}"
: "${NOTARYTOOL_PROFILE:?Set the existing local notarytool Keychain profile name}"
[[ $(uname -s) == Darwin ]]
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$APPLE_SIGNING_IDENTITY" == 'Developer ID Application:'* ]]
root=$(git rev-parse --show-toplevel)
cd "$root"
[[ -z $(git status --porcelain) ]] || { echo 'Use a clean tagged checkout.' >&2; exit 1; }
sha=$(git rev-parse HEAD)
[[ $(git rev-parse "refs/tags/$RELEASE_TAG^{commit}") == "$sha" ]]
mkdir -p dist/release
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
jq -n --arg tag "$RELEASE_TAG" --arg sha "$sha" --arg team "$APPLE_TEAM_ID" \
  '{tag:$tag,sourceSha:$sha,teamId:$team,artifacts:{}}' > "$work/manifest.json"
for arch in arm64 amd64; do
  RELEASE_TAG="$RELEASE_TAG" TARGET_OS=darwin TARGET_ARCH="$arch" bash scripts/release-build.sh
  binary="$root/dist/release/darwin-$arch/scratchpad-mcp"
  codesign --force --timestamp --options runtime --sign "$APPLE_SIGNING_IDENTITY" "$binary"
  codesign --verify --strict "$binary"
  codesign -dv --verbose=4 "$binary" 2> "$work/signature.txt"
  grep -Fxq "TeamIdentifier=$APPLE_TEAM_ID" "$work/signature.txt"
  grep -Fq 'Authority=Developer ID Application:' "$work/signature.txt"
  archive="$root/dist/release/scratchpad-mcp-$RELEASE_TAG-darwin-$arch.zip"
  receipt="$root/dist/release/notarization-darwin-$arch.json"
  # ditto must create a fresh archive, never update an older one.
  rm -f "$archive"
  ditto -c -k --keepParent "$binary" "$archive"
  xcrun notarytool submit "$archive" --keychain-profile "$NOTARYTOOL_PROFILE" \
    --wait --timeout 30m --output-format json > "$receipt"
  jq -e '.status == "Accepted" and (.id | type == "string")' "$receipt" >/dev/null
  archive_hash=$(shasum -a 256 "$archive" | awk '{print $1}')
  receipt_hash=$(shasum -a 256 "$receipt" | awk '{print $1}')
  jq --arg arch "$arch" --arg archive "$archive_hash" --arg receipt "$receipt_hash" \
    '.artifacts[$arch]={sha256:$archive,receiptSha256:$receipt}' "$work/manifest.json" > "$work/next.json"
  mv "$work/next.json" "$work/manifest.json"
done
mv "$work/manifest.json" dist/release/macos-release.json
printf 'Local signing and notarization complete for %s (%s). Upload the two ZIPs, two receipts and macos-release.json to the draft release.\n' "$RELEASE_TAG" "$sha"
