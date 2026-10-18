#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
app="$tmp/Scratchpad.app"
mkdir -p "$app/Contents"
info="$app/Contents/Info.plist"
cp "$repo_root/macos/Resources/Info.plist" "$info"
validate() { sh "$repo_root/macos/Scripts/validate-release.sh" "$app"; }
reject() { if validate >/dev/null 2>&1; then echo "validator accepted $1" >&2; exit 1; fi; }
validate
/usr/libexec/PlistBuddy -c 'Delete :QueueSchemaMinReadable' "$info"
reject 'missing schema metadata'
cp "$repo_root/macos/Resources/Info.plist" "$info"
/usr/libexec/PlistBuddy -c 'Set :QueueSchemaMinReadable 2' "$info"
reject 'incompatible schema range'
cp "$repo_root/macos/Resources/Info.plist" "$info"
/usr/libexec/PlistBuddy -c 'Add :SUFeedURL string https://example.invalid/appcast.xml' "$info"
reject 'retired updater metadata'
cp "$repo_root/macos/Resources/Info.plist" "$info"
mkdir -p "$app/Contents/Frameworks/Sparkle.framework"
reject 'retired updater framework'
echo "release metadata rejects missing/incompatible schemas and updater remnants"
