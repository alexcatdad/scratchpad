#!/usr/bin/env bash
set -euo pipefail

# Build, sign, notarize, staple, and checksum the native tray app locally.
# Signing and notarization credentials stay in the operator's Keychain/profile.
: "${RELEASE_TAG:?Set an existing stable vX.Y.Z tag}"
: "${APPLE_SIGNING_IDENTITY:?Set the local Developer ID Application identity}"
: "${APPLE_TEAM_ID:?Set the expected public Apple team ID}"
: "${KEYCHAIN_PROFILE:?Set the readable Data Protection Keychain provisioning profile}"
: "${KEYCHAIN_ACCESS_GROUP:?Set the authorized queue-key access group}"
: "${NOTARYTOOL_PROFILE:?Set the existing local notarytool Keychain profile name}"
[[ $(uname -s) == Darwin ]]
[[ $(uname -m) == arm64 ]] || { echo 'Build the Scratchpad app release on Apple Silicon.' >&2; exit 1; }
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$APPLE_SIGNING_IDENTITY" == 'Developer ID Application:'* ]]

root=$(git rev-parse --show-toplevel)
cd "$root"
[[ -z $(git status --porcelain) ]] || { echo 'Use a clean tagged checkout.' >&2; exit 1; }
sha=$(git rev-parse HEAD)
[[ $(git rev-parse "refs/tags/$RELEASE_TAG^{commit}") == "$sha" ]]
version=${RELEASE_TAG#v}
mkdir -p dist/release
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
app="$work/Scratchpad.app"

APP_VERSION="$version" \
BUILD_NUMBER="${BUILD_NUMBER:-1}" \
SIGNING_IDENTITY="$APPLE_SIGNING_IDENTITY" \
KEYCHAIN_PROFILE="$KEYCHAIN_PROFILE" \
KEYCHAIN_ACCESS_GROUP="$KEYCHAIN_ACCESS_GROUP" \
macos/Scripts/package-app.sh "$app"

codesign --verify --deep --strict --verbose=2 "$app"
codesign -dv --verbose=4 "$app" 2> "$work/signature.txt"
grep -Fxq "TeamIdentifier=$APPLE_TEAM_ID" "$work/signature.txt"
grep -Fq 'Authority=Developer ID Application:' "$work/signature.txt"

submission="$work/notarization.json"
ditto -c -k --sequesterRsrc --keepParent "$app" "$work/Scratchpad-notarization.zip"
xcrun notarytool submit "$work/Scratchpad-notarization.zip" \
  --keychain-profile "$NOTARYTOOL_PROFILE" --wait --timeout 30m \
  --output-format json > "$submission"
jq -e '.status == "Accepted" and (.id | type == "string")' "$submission" >/dev/null
xcrun stapler staple "$app"
xcrun stapler validate "$app"
spctl --assess --type execute --verbose=2 "$app"
codesign --verify --deep --strict --verbose=2 "$app"

archive="$root/dist/release/scratchpad-$RELEASE_TAG-darwin-arm64.zip"
receipt="$root/dist/release/notarization-app-$RELEASE_TAG.json"
manifest="$root/dist/release/macos-app-release.json"
rm -f "$archive" "$receipt" "$manifest"
ditto -c -k --sequesterRsrc --keepParent "$app" "$archive"
cp "$submission" "$receipt"
archive_hash=$(shasum -a 256 "$archive" | awk '{print $1}')
receipt_hash=$(shasum -a 256 "$receipt" | awk '{print $1}')
jq -n --arg tag "$RELEASE_TAG" --arg sha "$sha" --arg team "$APPLE_TEAM_ID" \
  --arg archive "$archive_hash" --arg receipt "$receipt_hash" \
  '{tag:$tag,sourceSha:$sha,teamId:$team,architecture:"arm64",artifacts:{app:{sha256:$archive,receiptSha256:$receipt}}}' \
  > "$manifest"
printf 'Created notarized app archive %s and manifest %s. No release assets or tap files were published.\n' "$archive" "$manifest"
