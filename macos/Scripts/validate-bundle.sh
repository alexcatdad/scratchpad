#!/bin/sh
set -eu

app=${1:?usage: validate-bundle.sh Scratchpad.app}
test -d "$app" || { echo "missing app bundle" >&2; exit 1; }
info="$app/Contents/Info.plist"
service="$app/Contents/Helpers/scratchpad-service"
mcp="$app/Contents/Helpers/scratchpad-mcp"
keychain_helper="$app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS/scratchpad-keychain"
keychain_info="$app/Contents/Helpers/scratchpad-keychain.app/Contents/Info.plist"
test -x "$service" || { echo "missing executable service helper" >&2; exit 1; }
test -x "$mcp" || { echo "missing executable MCP helper" >&2; exit 1; }
test -x "$keychain_helper" || { echo "missing executable Keychain helper" >&2; exit 1; }
test -f "$keychain_info" || { echo "missing Keychain helper app metadata" >&2; exit 1; }
test "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$keychain_info")" = "com.alexcatdad.scratchpad.keychain-helper" || {
  echo "unexpected Keychain helper bundle identifier" >&2
  exit 1
}

version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$info")
build=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$info")
schema=$(/usr/libexec/PlistBuddy -c 'Print :QueueSchemaVersion' "$info")
min=$(/usr/libexec/PlistBuddy -c 'Print :QueueSchemaMinReadable' "$info")
max=$(/usr/libexec/PlistBuddy -c 'Print :QueueSchemaMaxReadable' "$info")
case "$schema:$min:$max" in *[!0-9:]*|:*|*:) echo "invalid bundle queue schema metadata" >&2; exit 1;; esac
test "$schema" -gt 0 && test "$min" -gt 0 && test "$min" -le "$schema" && test "$schema" -le "$max" || {
  echo "bundle readable range does not include its queue schema" >&2
  exit 1
}

expected_mcp="scratchpad-mcp $version (build $build)"
expected_service="scratchpad-service $version (build $build; queue-schema $schema)"
actual_mcp=$("$mcp" --build-info)
actual_service=$("$service" --version)
if invalid_keychain_output=$("$keychain_helper" invalid-arguments 2>/dev/null); then
  keychain_invalid_status=0
else
  keychain_invalid_status=$?
fi
test "$actual_mcp" = "$expected_mcp" || {
  echo "MCP version provenance does not match bundle metadata" >&2
  exit 1
}
test "$actual_service" = "$expected_service" || {
  echo "service version or queue schema does not match bundle metadata" >&2
  exit 1
}
test -z "$invalid_keychain_output" || {
  echo "Keychain helper emitted stdout for invalid arguments" >&2
  exit 1
}
test "$keychain_invalid_status" -eq 1 || {
  echo "Keychain helper did not reject malformed arguments before Keychain access" >&2
  exit 1
}

echo "Bundle version, build number, and queue schema match both helper binaries."
