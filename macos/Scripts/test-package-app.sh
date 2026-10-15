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

cat >"$tmp/profile.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>ApplicationIdentifierPrefix</key><array><string>PREFIX1234</string></array>
  <key>TeamIdentifier</key><array><string>TEAMID1234</string></array>
  <key>Entitlements</key><dict>
    <key>com.apple.application-identifier</key><string>PREFIX1234.com.alexcatdad.scratchpad.keychain-helper</string>
    <key>keychain-access-groups</key><array><string>PREFIX1234.com.alexcatdad.scratchpad.queue</string></array>
  </dict>
</dict></plist>
PLIST
sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/profile.plist" PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/helper.entitlements"
plutil -lint "$tmp/helper.entitlements" >/dev/null
test "$(/usr/libexec/PlistBuddy -c 'Print :com.apple.application-identifier' "$tmp/helper.entitlements")" = \
  "PREFIX1234.com.alexcatdad.scratchpad.keychain-helper"
test "$(/usr/libexec/PlistBuddy -c 'Print :com.apple.developer.team-identifier' "$tmp/helper.entitlements")" = "TEAMID1234"
if /usr/libexec/PlistBuddy -c 'Print :application-identifier' "$tmp/helper.entitlements" >/dev/null 2>&1; then
  echo "native helper entitlements unexpectedly used the iOS application-identifier key" >&2
  exit 1
fi

cp "$tmp/profile.plist" "$tmp/invalid-profile.plist"
/usr/libexec/PlistBuddy -c 'Set :Entitlements:com.apple.application-identifier PREFIX1234.com.other.app' "$tmp/invalid-profile.plist"
if sh "$repo_root/macos/Scripts/create-keychain-helper-entitlements.sh" \
  "$tmp/invalid-profile.plist" PREFIX1234.com.alexcatdad.scratchpad.queue "$tmp/rejected.entitlements" >/dev/null 2>&1; then
  echo "entitlement generator accepted an unrelated profile application identifier" >&2
  exit 1
fi
test ! -e "$tmp/rejected.entitlements"

echo "package script safely preserved output and generated native entitlements from synthetic profile metadata"
