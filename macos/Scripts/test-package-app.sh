#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-package-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
mkdir -p "$tmp/bin" "$tmp/existing/Scratchpad.app/Contents" "$tmp/non-app.app"
cp "$repo_root/macos/Resources/Info.plist" "$tmp/existing/Scratchpad.app/Contents/Info.plist"
printf 'prior bundle marker\n' > "$tmp/existing/Scratchpad.app/Contents/marker"

cat > "$tmp/bin/swift" <<'SH'
#!/bin/sh
touch "$SWIFT_CALLED"
exit 17
SH
chmod +x "$tmp/bin/swift"
export PATH="$tmp/bin:$PATH"
export SWIFT_CALLED="$tmp/swift-called"

if "$repo_root/macos/Scripts/package-app.sh" "$tmp/existing/Scratchpad.app" >/dev/null 2>&1; then
  echo "package script unexpectedly succeeded with a failing build tool" >&2
  exit 1
fi
test -f "$tmp/existing/Scratchpad.app/Contents/marker"
test "$(cat "$tmp/existing/Scratchpad.app/Contents/marker")" = "prior bundle marker"

unset KEYCHAIN_PROFILE KEYCHAIN_ACCESS_GROUP
SIGNING_IDENTITY='Developer ID Application: Synthetic (TEAMID)' \
  "$repo_root/macos/Scripts/package-app.sh" "$tmp/existing/Scratchpad.app" >"$tmp/profile-error" 2>&1 && {
    echo "signed package script unexpectedly proceeded without a Keychain provisioning profile" >&2
    exit 1
  }
rg -q 'KEYCHAIN_PROFILE is required' "$tmp/profile-error"
test -f "$tmp/existing/Scratchpad.app/Contents/marker"

rm -f "$SWIFT_CALLED"
if "$repo_root/macos/Scripts/package-app.sh" "$tmp/non-app.app" >/dev/null 2>&1; then
  echo "package script accepted an existing non-app destination" >&2
  exit 1
fi
test ! -e "$SWIFT_CALLED"

if "$repo_root/macos/Scripts/package-app.sh" / >/dev/null 2>&1; then
  echo "package script accepted filesystem root as the destination" >&2
  exit 1
fi

mkdir -p "$tmp/fake-bin"
cat > "$tmp/fake-bin/security" <<'SH'
#!/bin/sh
printf '  1) %s "%s"\n     1 valid identities found\n' "$PROFILE_CERT_SHA1" "$PROFILE_SIGNING_IDENTITY"
SH
chmod +x "$tmp/fake-bin/security"
export PATH="$tmp/fake-bin:$PATH"
synthetic_certificate='synthetic developer certificate bytes'
synthetic_certificate_data=$(printf '%s' "$synthetic_certificate" | base64 | tr -d '\n')
synthetic_certificate_hash=$(printf '%s' "$synthetic_certificate" | shasum -a 1 | awk '{print toupper($1)}')
synthetic_identity='Developer ID Application: Synthetic (TEAMID1234)'
export PROFILE_CERT_SHA1="$synthetic_certificate_hash" PROFILE_SIGNING_IDENTITY="$synthetic_identity"

write_profile() {
  profile_group=$1
  profile_expiration=$2
  profile_certificate=$3
  cat > "$tmp/profile.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>ApplicationIdentifierPrefix</key><array><string>PREFIX1234</string></array>
  <key>TeamIdentifier</key><array><string>TEAMID1234</string></array>
  <key>ExpirationDate</key><date>$profile_expiration</date>
  <key>DeveloperCertificates</key><array><data>$profile_certificate</data></array>
  <key>Entitlements</key><dict>
    <key>com.apple.application-identifier</key><string>PREFIX1234.com.alexcatdad.scratchpad.keychain-helper</string>
    <key>keychain-access-groups</key><array><string>$profile_group</string></array>
  </dict>
</dict></plist>
PLIST
}

write_profile PREFIX1234.com.alexcatdad.scratchpad.queue 2099-12-31T23:59:59Z "$synthetic_certificate_data"
sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/profile.plist" "$synthetic_identity" PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/helper.entitlements"
plutil -lint "$tmp/helper.entitlements" >/dev/null
test "$(/usr/libexec/PlistBuddy -c 'Print :com.apple.application-identifier' "$tmp/helper.entitlements")" = \
  "PREFIX1234.com.alexcatdad.scratchpad.keychain-helper"
test "$(/usr/libexec/PlistBuddy -c 'Print :com.apple.developer.team-identifier' "$tmp/helper.entitlements")" = "TEAMID1234"
if /usr/libexec/PlistBuddy -c 'Print :application-identifier' "$tmp/helper.entitlements" >/dev/null 2>&1; then
  echo "native helper entitlements unexpectedly used the iOS application-identifier key" >&2
  exit 1
fi

write_profile PREFIX1234.* 2099-12-31T23:59:59Z "$synthetic_certificate_data"
sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/profile.plist" "$synthetic_identity" PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/wildcard.entitlements"
test "$(/usr/libexec/PlistBuddy -c 'Print :keychain-access-groups:0' "$tmp/wildcard.entitlements")" = \
  "PREFIX1234.com.alexcatdad.scratchpad.queue"

write_profile PREFIX1234.com.alexcatdad.* 2099-12-31T23:59:59Z "$synthetic_certificate_data"
if sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/profile.plist" "$synthetic_identity" PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/rejected-broad-wildcard.entitlements" >/dev/null 2>&1; then
  echo "entitlement generator accepted a broader-than-prefix keychain group wildcard" >&2
  exit 1
fi
test ! -e "$tmp/rejected-broad-wildcard.entitlements"

write_profile PREFIX1234.com.alexcatdad.scratchpad.queue 2000-01-01T00:00:00Z "$synthetic_certificate_data"
if sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/profile.plist" "$synthetic_identity" PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/rejected-expired.entitlements" >/dev/null 2>&1; then
  echo "entitlement generator accepted an expired profile" >&2
  exit 1
fi
test ! -e "$tmp/rejected-expired.entitlements"

wrong_certificate_data=$(printf '%s' 'different developer certificate bytes' | base64 | tr -d '\n')
write_profile PREFIX1234.com.alexcatdad.scratchpad.queue 2099-12-31T23:59:59Z "$wrong_certificate_data"
if sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/profile.plist" "$synthetic_identity" PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/rejected-certificate.entitlements" >/dev/null 2>&1; then
  echo "entitlement generator accepted a profile that excludes the signing certificate" >&2
  exit 1
fi
test ! -e "$tmp/rejected-certificate.entitlements"

write_profile PREFIX1234.com.alexcatdad.scratchpad.queue 2099-12-31T23:59:59Z "$synthetic_certificate_data"
if sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/profile.plist" 'Apple Development: Synthetic (TEAMID1234)' PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/rejected-identity-type.entitlements" >/dev/null 2>&1; then
  echo "entitlement generator accepted a non-Developer-ID signing identity" >&2
  exit 1
fi
test ! -e "$tmp/rejected-identity-type.entitlements"

cat > "$tmp/fake-bin/codesign" <<'SH'
#!/bin/sh
for argument do target=$argument; done
printf '%s\n' "$target" >> "$SIGN_LOG"
SH
chmod +x "$tmp/fake-bin/codesign"
export SIGN_LOG="$tmp/codesign.log"
framework="$tmp/Sparkle.framework"
mkdir -p "$framework/Versions/B/Updater.app/Contents/MacOS" \
  "$framework/Versions/B/XPCServices/Downloader.xpc/Contents/MacOS"
touch "$framework/Versions/B/Autoupdate" \
  "$framework/Versions/B/Updater.app/Contents/MacOS/Updater" \
  "$framework/Versions/B/XPCServices/Downloader.xpc/Contents/MacOS/Downloader"
PATH="$tmp/fake-bin:$PATH" sh "$repo_root/macos/Scripts/sign-sparkle-framework.sh" "$framework" "$synthetic_identity"
test "$(head -n 1 "$SIGN_LOG")" = "$framework/Versions/B/Autoupdate"
rg -q -F "$framework/Versions/B/Updater.app" "$SIGN_LOG"
rg -q -F "$framework/Versions/B/XPCServices/Downloader.xpc" "$SIGN_LOG"
if rg -q -x -F "$framework" "$SIGN_LOG"; then
  echo "nested Sparkle signer unexpectedly signed the outer framework" >&2
  exit 1
fi
rm -f "$framework/Versions/B/Autoupdate"
if PATH="$tmp/fake-bin:$PATH" sh "$repo_root/macos/Scripts/sign-sparkle-framework.sh" "$framework" "$synthetic_identity" >/dev/null 2>&1; then
  echo "nested Sparkle signer accepted a missing Autoupdate executable" >&2
  exit 1
fi

cp "$tmp/profile.plist" "$tmp/invalid-profile.plist"
/usr/libexec/PlistBuddy -c 'Set :Entitlements:com.apple.application-identifier PREFIX1234.com.other.app' "$tmp/invalid-profile.plist"
if sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/invalid-profile.plist" "$synthetic_identity" PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/rejected-app-id.entitlements" >/dev/null 2>&1; then
  echo "entitlement generator accepted an unrelated profile application identifier" >&2
  exit 1
fi
test ! -e "$tmp/rejected-app-id.entitlements"

echo "package script preserves prior output; signing preflight validates identity, profile scope, certificate, and expiry; Sparkle nested code signing covers Autoupdate and inner bundles"
