#!/bin/sh
set -eu
app=${1:?usage: validate-release.sh Scratchpad.app}
info="$app/Contents/Info.plist"
test -f "$info" || { echo "missing app metadata" >&2; exit 1; }
for key in SUFeedURL SUPublicEDKey SUEnableAutomaticChecks SUAllowsAutomaticUpdates; do
  if /usr/libexec/PlistBuddy -c "Print :$key" "$info" >/dev/null 2>&1; then
    echo "retired updater metadata remains: $key" >&2; exit 1
  fi
done
test ! -e "$app/Contents/Frameworks/Sparkle.framework" || { echo "retired updater framework remains" >&2; exit 1; }
for key in CFBundleShortVersionString CFBundleVersion; do
  value=$(/usr/libexec/PlistBuddy -c "Print :$key" "$info")
  test -n "$value" || { echo "missing $key" >&2; exit 1; }
done
schema=$(/usr/libexec/PlistBuddy -c 'Print :QueueSchemaVersion' "$info")
min=$(/usr/libexec/PlistBuddy -c 'Print :QueueSchemaMinReadable' "$info")
max=$(/usr/libexec/PlistBuddy -c 'Print :QueueSchemaMaxReadable' "$info")
case "$schema:$min:$max" in *[!0-9:]*|:*|*:) echo "invalid bundle schema metadata" >&2; exit 1;; esac
test "$min" -gt 0 && test "$schema" -ge "$min" && test "$schema" -le "$max" || { echo "invalid bundle schema range" >&2; exit 1; }
echo "Homebrew release metadata is valid; signing and installed upgrade acceptance are separate checks."
