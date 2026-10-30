#!/bin/sh
# Builds and Developer ID signs Scratchpad.app for release-tools, then archives
# it as the single declared artifact. release-tools verifies, notarizes and
# staples the archive afterwards.
set -eu

: "${RELEASE_OUT:?Set RELEASE_OUT}"
: "${RELEASE_VERSION:?Set RELEASE_VERSION}"
: "${MACOS_SHORT_VERSION:?Set MACOS_SHORT_VERSION}"
: "${MACOS_BUNDLE_VERSION:?Set MACOS_BUNDLE_VERSION}"
: "${MACOS_SIGNING_IDENTITY:?Set MACOS_SIGNING_IDENTITY}"

# The keychain helper's Developer ID provisioning profile stays outside the
# checkout; it must authorize the signing identity's certificate.
profile=${SCRATCHPAD_KEYCHAIN_PROFILE:-$HOME/.config/scratchpad/keychain-helper.provisionprofile}
team=$(printf '%s' "$MACOS_SIGNING_IDENTITY" | sed -n 's/.*(\([A-Z0-9]\{10\}\))$/\1/p')
[ -n "$team" ] || { echo "Cannot read the team ID from the signing identity" >&2; exit 1; }

work=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-app.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

APP_VERSION="$MACOS_SHORT_VERSION" \
BUILD_NUMBER="$MACOS_BUNDLE_VERSION" \
SIGNING_IDENTITY="$MACOS_SIGNING_IDENTITY" \
KEYCHAIN_PROFILE="$profile" \
KEYCHAIN_ACCESS_GROUP="$team.com.alexcatdad.scratchpad" \
DEVELOPER_DIR="${DEVELOPER_DIR:-$(xcode-select -p)}" \
  sh macos/Scripts/package-app.sh "$work/Scratchpad.app"

ditto -c -k --keepParent "$work/Scratchpad.app" "$RELEASE_OUT/scratchpad-$RELEASE_VERSION-darwin-arm64.zip"
