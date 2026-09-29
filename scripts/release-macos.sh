#!/usr/bin/env bash
set -euo pipefail

# Called only on the isolated macOS release runner. Never print secret values.
: "${RELEASE_TAG:?Missing release tag}"
: "${TARGET_ARCH:?Missing target architecture}"
: "${APPLE_CERTIFICATE_P12_BASE64:?Missing Developer ID certificate}"
: "${APPLE_CERTIFICATE_PASSWORD:?Missing certificate password}"
: "${APPLE_SIGNING_IDENTITY:?Missing Developer ID signing identity}"
: "${APPLE_ID:?Missing Apple account}"
: "${APPLE_TEAM_ID:?Missing Apple team ID}"
: "${APPLE_APP_SPECIFIC_PASSWORD:?Missing notarization password}"
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "$TARGET_ARCH" == arm64 || "$TARGET_ARCH" == amd64 ]]
[[ "$APPLE_SIGNING_IDENTITY" == 'Developer ID Application:'* ]]
root=$(git rev-parse --show-toplevel)
work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/scratchpad-sign.XXXXXX")
keychain="$work/release.keychain-db"
cleanup() {
  security delete-keychain "$keychain" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
password=$(openssl rand -hex 32)
printf '%s' "$APPLE_CERTIFICATE_P12_BASE64" | base64 --decode > "$work/certificate.p12"
security create-keychain -p "$password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$password" "$keychain"
security import "$work/certificate.p12" -k "$keychain" -P "$APPLE_CERTIFICATE_PASSWORD" \
  -T /usr/bin/codesign -T /usr/bin/security >/dev/null
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$password" "$keychain" >/dev/null
binary="$root/dist/release/darwin-${TARGET_ARCH}/scratchpad-mcp"
codesign --force --timestamp --options runtime --keychain "$keychain" \
  --sign "$APPLE_SIGNING_IDENTITY" "$binary"
codesign --verify --strict --verbose=2 "$binary"
codesign -dv --verbose=4 "$binary" 2> "$work/signature.txt"
grep -Fq 'Authority=Developer ID Application:' "$work/signature.txt"
grep -Fq "TeamIdentifier=$APPLE_TEAM_ID" "$work/signature.txt"
grep -Fq 'runtime' "$work/signature.txt"
archive="$root/dist/release/scratchpad-mcp-${RELEASE_TAG}-darwin-${TARGET_ARCH}.zip"
ditto -c -k --keepParent "$binary" "$archive"
receipt="$root/dist/release/notarization-darwin-${TARGET_ARCH}.json"
xcrun notarytool submit "$archive" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" \
  --password "$APPLE_APP_SPECIFIC_PASSWORD" --wait --timeout 30m --output-format json > "$receipt"
jq -e '.status == "Accepted" and (.id | type == "string")' "$receipt" >/dev/null
# CLI Mach-O files and ZIP archives cannot be stapled. Publish the exact accepted
# ZIP; Gatekeeper retrieves its ticket online. Never rewrite it after acceptance.
if [[ $(uname -m) == "$TARGET_ARCH" || ( $(uname -m) == x86_64 && "$TARGET_ARCH" == amd64 ) ]]; then
  "$binary" --version | grep -Fx "scratchpad-mcp ${RELEASE_TAG#v}"
fi
