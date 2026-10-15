#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
requested_app=${1:?usage: package-app.sh /output/Scratchpad.app}
developer_dir=${DEVELOPER_DIR:-/Library/Developer/CommandLineTools}
version=${APP_VERSION:-1.0.0}
build_number=${BUILD_NUMBER:-1}
export DEVELOPER_DIR="$developer_dir"

case "$version" in
  [0-9]* ) ;;
  * ) echo "APP_VERSION must start with a digit" >&2; exit 1 ;;
esac
case "$version" in *[!A-Za-z0-9.+-]* ) echo "APP_VERSION contains unsupported characters" >&2; exit 1;; esac
case "$build_number" in
  ''|*[!0-9.]*|.*|*.) echo "BUILD_NUMBER must be a dotted numeric bundle build number" >&2; exit 1 ;;
  *..*) echo "BUILD_NUMBER must not contain empty components" >&2; exit 1 ;;
esac

if [ -n "${SIGNING_IDENTITY:-}" ]; then
  [ -n "${KEYCHAIN_PROFILE:-}" ] || { echo "KEYCHAIN_PROFILE is required for signed builds that use the Data Protection Keychain" >&2; exit 1; }
  [ -r "$KEYCHAIN_PROFILE" ] || { echo "KEYCHAIN_PROFILE must name a readable provisioning profile" >&2; exit 1; }
  [ -n "${KEYCHAIN_ACCESS_GROUP:-}" ] || { echo "KEYCHAIN_ACCESS_GROUP is required for signed builds that use the Data Protection Keychain" >&2; exit 1; }
fi

case "$requested_app" in
  /) echo "refusing to package over the filesystem root" >&2; exit 1 ;;
  /*) absolute_requested=$requested_app ;;
  *) absolute_requested="$PWD/$requested_app" ;;
esac
requested_parent=$(dirname -- "$absolute_requested")
requested_name=$(basename -- "$absolute_requested")
case "$requested_name" in
  *.app) ;;
  *) echo "destination must end with .app" >&2; exit 1 ;;
esac
mkdir -p "$requested_parent"
parent=$(CDPATH= cd -P -- "$requested_parent" && pwd)
app="$parent/$requested_name"
case "$app" in /|/.) echo "refusing an unsafe destination" >&2; exit 1;; esac

check_destination() {
  if [ -L "$app" ]; then
    echo "refusing to replace a symbolic-link destination" >&2
    return 1
  fi
  if [ -e "$app" ]; then
    if [ ! -d "$app" ] || [ ! -f "$app/Contents/Info.plist" ]; then
      echo "refusing to replace an existing non-app destination" >&2
      return 1
    fi
    /usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app/Contents/Info.plist" >/dev/null 2>&1 || {
      echo "refusing to replace an invalid app bundle" >&2
      return 1
    }
    existing_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app/Contents/Info.plist")
    if [ "$existing_id" != "com.alexcatdad.scratchpad" ]; then
      echo "refusing to replace a different app bundle" >&2
      return 1
    fi
  fi
}

check_destination
stage_root=$(mktemp -d "$parent/.scratchpad-package.XXXXXX")
candidate="$stage_root/Scratchpad.app"
backup="$stage_root/previous.app"
backup_moved=0
cleanup() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ "$backup_moved" -eq 1 ] && [ ! -e "$app" ] && [ ! -L "$app" ] && [ -e "$backup" ]; then
    mv "$backup" "$app" || echo "failed to restore the prior app bundle at $app" >&2
  fi
  rm -rf "$stage_root"
  exit "$result"
}
trap cleanup EXIT HUP INT TERM

if [ -n "${SIGNING_IDENTITY:-}" ]; then
  security cms -D -i "$KEYCHAIN_PROFILE" -o "$stage_root/keychain-profile.plist" >/dev/null 2>&1 || {
    echo "KEYCHAIN_PROFILE is not a valid provisioning profile" >&2
    exit 1
  }
  sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
    "$stage_root/keychain-profile.plist" "$SIGNING_IDENTITY" "$KEYCHAIN_ACCESS_GROUP" "$stage_root/keychain-access.entitlements"
  profile_team=$(/usr/libexec/PlistBuddy -c 'Print :TeamIdentifier:0' "$stage_root/keychain-profile.plist")
fi

swift build --package-path "$repo_root/macos" --product Scratchpad
swift build --package-path "$repo_root/macos" --product scratchpad-keychain
bin=$(swift build --package-path "$repo_root/macos" --show-bin-path)

mkdir -p "$stage_root/helpers"
go build -C "$repo_root/mcp" -ldflags "-X main.version=$version -X main.buildNumber=$build_number" -o "$stage_root/helpers/scratchpad-service" ./cmd/scratchpad-service
go build -C "$repo_root/mcp" -ldflags "-X main.version=$version -X main.buildNumber=$build_number" -o "$stage_root/helpers/scratchpad-mcp" ./cmd/scratchpad-mcp

mkdir -p "$candidate/Contents/MacOS" "$candidate/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS" "$candidate/Contents/Library/LaunchAgents" "$candidate/Contents/Frameworks"
cp "$bin/Scratchpad" "$candidate/Contents/MacOS/Scratchpad"
cp "$bin/scratchpad-keychain" "$candidate/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS/scratchpad-keychain"
cp "$repo_root/macos/Resources/KeychainHelper-Info.plist" "$candidate/Contents/Helpers/scratchpad-keychain.app/Contents/Info.plist"
cp "$stage_root/helpers/scratchpad-service" "$candidate/Contents/Helpers/scratchpad-service"
cp "$stage_root/helpers/scratchpad-mcp" "$candidate/Contents/Helpers/scratchpad-mcp"
cp "$repo_root/macos/Resources/Info.plist" "$candidate/Contents/Info.plist"
cp "$repo_root/macos/Resources/com.alexcatdad.scratchpad.service.plist" "$candidate/Contents/Library/LaunchAgents/com.alexcatdad.scratchpad.service.plist"

framework=$(find "$repo_root/macos/.build/artifacts" -type d -name Sparkle.framework -print -quit 2>/dev/null || true)
test -n "$framework" || { echo "SwiftPM did not produce Sparkle.framework" >&2; exit 1; }
ditto "$framework" "$candidate/Contents/Frameworks/Sparkle.framework"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$candidate/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $build_number" "$candidate/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$candidate/Contents/Helpers/scratchpad-keychain.app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $build_number" "$candidate/Contents/Helpers/scratchpad-keychain.app/Contents/Info.plist"

if [ -n "${SPARKLE_FEED_URL:-}" ]; then
  /usr/libexec/PlistBuddy -c "Add :SUFeedURL string $SPARKLE_FEED_URL" "$candidate/Contents/Info.plist"
fi
if [ -n "${SPARKLE_PUBLIC_KEY:-}" ]; then
  /usr/libexec/PlistBuddy -c "Add :SUPublicEDKey string $SPARKLE_PUBLIC_KEY" "$candidate/Contents/Info.plist"
fi

plutil -lint "$candidate/Contents/Info.plist" "$candidate/Contents/Library/LaunchAgents/com.alexcatdad.scratchpad.service.plist"
plutil -lint "$candidate/Contents/Helpers/scratchpad-keychain.app/Contents/Info.plist"
"$repo_root/macos/Scripts/validate-bundle.sh" "$candidate"

if [ -n "${SIGNING_IDENTITY:-}" ]; then
  cp "$KEYCHAIN_PROFILE" "$candidate/Contents/Helpers/scratchpad-keychain.app/Contents/embedded.provisionprofile"
  # Sign nested Sparkle code from the deepest bundle outward. The app is signed
  # last, so its resource seal covers the finished helper and framework layout.
  sh "$repo_root/macos/Scripts/sign-sparkle-framework.sh" \
    "$candidate/Contents/Frameworks/Sparkle.framework" "$SIGNING_IDENTITY"
  codesign --force --options runtime --timestamp --entitlements "$stage_root/keychain-access.entitlements" --sign "$SIGNING_IDENTITY" "$candidate/Contents/Helpers/scratchpad-keychain.app"
  helper_team=$(codesign -dv --verbose=4 "$candidate/Contents/Helpers/scratchpad-keychain.app" 2>&1 | sed -n 's/^TeamIdentifier=//p')
  [ "$helper_team" = "$profile_team" ] || { echo "KEYCHAIN_PROFILE team does not match SIGNING_IDENTITY" >&2; exit 1; }
  for helper in "$candidate/Contents/Helpers/scratchpad-mcp" "$candidate/Contents/Helpers/scratchpad-service"; do
    codesign --force --options runtime --timestamp --sign "$SIGNING_IDENTITY" "$helper"
  done
  codesign --force --options runtime --timestamp --sign "$SIGNING_IDENTITY" "$candidate/Contents/Frameworks/Sparkle.framework"
  codesign --force --options runtime --timestamp --sign "$SIGNING_IDENTITY" "$candidate"
  codesign --verify --deep --strict --verbose=2 "$candidate"
fi

check_destination
if [ -e "$app" ]; then
  mv "$app" "$backup"
  backup_moved=1
fi
if ! mv "$candidate" "$app"; then
  if [ "$backup_moved" -eq 1 ]; then mv "$backup" "$app"; backup_moved=0; fi
  echo "could not install the completed app bundle at $app" >&2
  exit 1
fi
backup_moved=0

if [ -n "${SIGNING_IDENTITY:-}" ]; then
  signing_status="signed with $SIGNING_IDENTITY"
else
  signing_status="unsigned local build"
fi
echo "Created $app ($signing_status). Notarization was not attempted."
