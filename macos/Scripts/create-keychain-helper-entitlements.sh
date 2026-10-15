#!/bin/sh
set -eu

profile=${1:?usage: create-keychain-helper-entitlements.sh decoded-profile.plist access-group output.entitlements}
access_group=${2:?usage: create-keychain-helper-entitlements.sh decoded-profile.plist access-group output.entitlements}
output=${3:?usage: create-keychain-helper-entitlements.sh decoded-profile.plist access-group output.entitlements}
helper_bundle_id=com.alexcatdad.scratchpad.keychain-helper

profile_team=$(/usr/libexec/PlistBuddy -c 'Print :TeamIdentifier:0' "$profile" 2>/dev/null || true)
profile_application_id=$(/usr/libexec/PlistBuddy -c 'Print :Entitlements:com.apple.application-identifier' "$profile" 2>/dev/null || true)
case "$profile_team" in ''|*[!A-Za-z0-9]*) echo "provisioning profile has no valid team identifier" >&2; exit 1;; esac
case "$access_group" in ''|*[!A-Za-z0-9.-]*) echo "access group has unsupported characters" >&2; exit 1;; esac

profile_group_found=0
for group_index in 0 1 2 3 4 5 6 7; do
  profile_group=$(/usr/libexec/PlistBuddy -c "Print :Entitlements:keychain-access-groups:$group_index" "$profile" 2>/dev/null || true)
  if [ "$profile_group" = "$access_group" ]; then profile_group_found=1; break; fi
done
[ "$profile_group_found" -eq 1 ] || { echo "provisioning profile does not authorize the access group" >&2; exit 1; }

application_prefix=
for prefix_index in 0 1 2 3 4 5 6 7; do
  prefix=$(/usr/libexec/PlistBuddy -c "Print :ApplicationIdentifierPrefix:$prefix_index" "$profile" 2>/dev/null || true)
  case "$prefix" in ''|*[!A-Za-z0-9]*) continue;; esac
  case "$profile_application_id" in
    "$prefix.$helper_bundle_id"|"$prefix.*") application_prefix=$prefix; break;;
  esac
done
[ -n "$application_prefix" ] || { echo "provisioning profile does not authorize the nested Keychain helper app identifier" >&2; exit 1; }

cat >"$output" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>com.apple.application-identifier</key><string>$application_prefix.$helper_bundle_id</string>
  <key>com.apple.developer.team-identifier</key><string>$profile_team</string>
  <key>keychain-access-groups</key><array><string>$access_group</string></array>
</dict></plist>
EOF
plutil -lint "$output" >/dev/null
