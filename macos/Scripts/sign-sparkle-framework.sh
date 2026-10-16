#!/bin/sh
set -eu

framework=${1:?usage: sign-sparkle-framework.sh Sparkle.framework Developer-ID-identity}
signing_identity=${2:?usage: sign-sparkle-framework.sh Sparkle.framework Developer-ID-identity}

autoupdate="$framework/Versions/B/Autoupdate"
[ -f "$autoupdate" ] || { echo "Sparkle framework is missing Versions/B/Autoupdate" >&2; exit 1; }

# Autoupdate is a Mach-O executable inside Updater.app, not a bundle directory.
# Sign it before signing the nested app and framework seals.
codesign --force --options runtime --timestamp --sign "$signing_identity" "$autoupdate"

find "$framework" -depth \( -name '*.xpc' -o -name '*.app' -o -name '*.framework' \) -type d -print | while IFS= read -r nested; do
  [ "$nested" = "$framework" ] && continue
  codesign --force --options runtime --timestamp --sign "$signing_identity" "$nested"
done
