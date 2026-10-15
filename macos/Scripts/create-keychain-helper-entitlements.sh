#!/bin/sh
set -eu

profile=${1:?usage: create-keychain-helper-entitlements.sh decoded-profile.plist signing-identity access-group output.entitlements}
signing_identity=${2:?usage: create-keychain-helper-entitlements.sh decoded-profile.plist signing-identity access-group output.entitlements}
access_group=${3:?usage: create-keychain-helper-entitlements.sh decoded-profile.plist signing-identity access-group output.entitlements}
output=${4:?usage: create-keychain-helper-entitlements.sh decoded-profile.plist signing-identity access-group output.entitlements}
helper_bundle_id=com.alexcatdad.scratchpad.keychain-helper

profile_team=$(/usr/libexec/PlistBuddy -c 'Print :TeamIdentifier:0' "$profile" 2>/dev/null || true)
profile_application_id=$(/usr/libexec/PlistBuddy -c 'Print :Entitlements:com.apple.application-identifier' "$profile" 2>/dev/null || true)
case "$profile_team" in ''|*[!A-Za-z0-9]*) echo "provisioning profile has no valid team identifier" >&2; exit 1;; esac
case "$access_group" in ''|*[!A-Za-z0-9.-]*) echo "access group has unsupported characters" >&2; exit 1;; esac
case "$signing_identity" in "Developer ID Application: "*) ;; *) echo "SIGNING_IDENTITY must be a Developer ID Application identity" >&2; exit 1;; esac

profile_expiration=$(plutil -extract ExpirationDate raw -o - "$profile" 2>/dev/null || true)
[ -n "$profile_expiration" ] || { echo "provisioning profile has no expiration date" >&2; exit 1; }
profile_expiration=$(printf '%s\n' "$profile_expiration" | sed -E 's/\.[0-9]+Z$/Z/')
expiration_epoch=$(date -j -u -f '%Y-%m-%dT%H:%M:%SZ' "$profile_expiration" +%s 2>/dev/null || true)
case "$expiration_epoch" in ''|*[!0-9]*) echo "provisioning profile has an invalid expiration date" >&2; exit 1;; esac
now_epoch=$(date -u +%s)
[ "$expiration_epoch" -gt "$now_epoch" ] || { echo "provisioning profile has expired" >&2; exit 1; }

identity_output=$(security find-identity -v -p codesigning 2>/dev/null) || {
  echo "could not inspect valid code-signing identities" >&2
  exit 1
}
identity_hash=$(printf '%s\n' "$identity_output" | awk -v identity="$signing_identity" '
  index($0, "\"" identity "\"") && $1 ~ /^[0-9]+\)$/ && $2 ~ /^[[:xdigit:]]+$/ {
    hash = toupper($2)
    count++
  }
  END { if (count != 1) exit 1; print hash }
') || { echo "SIGNING_IDENTITY is not one valid local code-signing identity" >&2; exit 1; }

temp_root=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-profile-validation.XXXXXX")
cleanup() {
  result=$?
  trap - EXIT HUP INT TERM
  rm -rf "$temp_root"
  exit "$result"
}
trap cleanup EXIT HUP INT TERM

plutil -extract DeveloperCertificates xml1 -o "$temp_root/developer-certificates.plist" "$profile" 2>/dev/null || {
  echo "provisioning profile has no DeveloperCertificates allowlist" >&2
  exit 1
}
certificate_count=$(awk '/<data>/{count++} END {print count+0}' "$temp_root/developer-certificates.plist")
[ "$certificate_count" -gt 0 ] || { echo "provisioning profile has no DeveloperCertificates allowlist" >&2; exit 1; }
identity_authorized=0
certificate_index=0
while [ "$certificate_index" -lt "$certificate_count" ]; do
  certificate_data=$(plutil -extract "DeveloperCertificates.$certificate_index" raw -o - "$profile" 2>/dev/null || true)
  if [ -n "$certificate_data" ]; then
    printf '%s' "$certificate_data" | /usr/bin/base64 -D > "$temp_root/profile-certificate.der"
    certificate_hash=$(shasum -a 1 "$temp_root/profile-certificate.der" | awk '{print toupper($1)}')
    if [ "$certificate_hash" = "$identity_hash" ]; then identity_authorized=1; fi
  fi
  certificate_index=$((certificate_index + 1))
done
[ "$identity_authorized" -eq 1 ] || {
  echo "provisioning profile does not authorize the selected SIGNING_IDENTITY certificate" >&2
  exit 1
}

application_prefix=
for prefix_index in 0 1 2 3 4 5 6 7; do
  prefix=$(/usr/libexec/PlistBuddy -c "Print :ApplicationIdentifierPrefix:$prefix_index" "$profile" 2>/dev/null || true)
  case "$prefix" in ''|*[!A-Za-z0-9]*) continue;; esac
  case "$profile_application_id" in
    "$prefix.$helper_bundle_id"|"$prefix.*") application_prefix=$prefix; break;;
  esac
done
[ -n "$application_prefix" ] || { echo "provisioning profile does not authorize the nested Keychain helper app identifier" >&2; exit 1; }

profile_group_found=0
for group_index in 0 1 2 3 4 5 6 7; do
  profile_group=$(/usr/libexec/PlistBuddy -c "Print :Entitlements:keychain-access-groups:$group_index" "$profile" 2>/dev/null || true)
  if [ "$profile_group" = "$access_group" ]; then profile_group_found=1; break; fi
  if [ "$profile_group" = "$application_prefix.*" ]; then
    case "$access_group" in "$application_prefix".?*) profile_group_found=1; break;; esac
  fi
done
[ "$profile_group_found" -eq 1 ] || { echo "provisioning profile does not authorize the access group" >&2; exit 1; }

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
