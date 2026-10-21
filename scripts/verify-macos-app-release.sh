#!/usr/bin/env bash
set -euo pipefail

: "${RELEASE_TAG:?Missing stable release tag}"
: "${EXPECTED_SHA:?Missing source commit}"
: "${APPLE_TEAM_ID:?Missing trusted signing team}"
: "${ASSET_DIR:?Missing downloaded asset directory}"
[[ $(uname -s) == Darwin && $(uname -m) == arm64 ]]
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
root=$(git rev-parse --show-toplevel)
archive="$ASSET_DIR/scratchpad-$RELEASE_TAG-darwin-arm64.zip"
receipt="$ASSET_DIR/notarization-app-$RELEASE_TAG.json"
manifest="$ASSET_DIR/macos-app-release.json"
jq -e --arg tag "$RELEASE_TAG" --arg sha "$EXPECTED_SHA" --arg team "$APPLE_TEAM_ID" \
  '.tag == $tag and .sourceSha == $sha and .teamId == $team and .architecture == "arm64"' "$manifest" >/dev/null
[[ $(shasum -a 256 "$archive" | awk '{print $1}') == $(jq -er '.artifacts.app.sha256' "$manifest") ]]
[[ $(shasum -a 256 "$receipt" | awk '{print $1}') == $(jq -er '.artifacts.app.receiptSha256' "$manifest") ]]
jq -e '.status == "Accepted" and (.id | type == "string" and length > 0)' "$receipt" >/dev/null
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# Reject unexpected roots and traversal before bsdtar's safe default extraction.
tar -tf "$archive" > "$work/entries"
awk '
  /^\// || /(^|\/)\.\.(\/|$)/ { exit 1 }
  !/^(Scratchpad\.app|__MACOSX)(\/|$)/ { exit 1 }
  END { if (NR == 0) exit 1 }
' "$work/entries"
tar -xf "$archive" -C "$work"
app="$work/Scratchpad.app"
sh "$root/macos/Scripts/validate-bundle.sh" "$app"
sh "$root/macos/Scripts/validate-release.sh" "$app"
[[ $(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist") == "${RELEASE_TAG#v}" ]]
codesign --verify --deep --strict --verbose=2 "$app"
for binary in "$app" "$app/Contents/Helpers/scratchpad-keychain.app" \
  "$app/Contents/Helpers/scratchpad-service" "$app/Contents/Helpers/scratchpad-mcp"; do
  codesign -dv --verbose=4 "$binary" 2> "$work/signature"
  grep -Fxq "TeamIdentifier=$APPLE_TEAM_ID" "$work/signature"
  grep -Fq 'Authority=Developer ID Application:' "$work/signature"
  grep -Fq 'runtime' "$work/signature"
done
[[ $(lipo -archs "$app/Contents/MacOS/Scratchpad") == arm64 ]]
xcrun stapler validate "$app"
spctl --assess --type execute --verbose=2 "$app"
printf 'Verified app %s from declared source %s, including Apple trust.\n' "$RELEASE_TAG" "$EXPECTED_SHA"
