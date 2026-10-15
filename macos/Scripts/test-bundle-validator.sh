#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-bundle-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
app="$tmp/Scratchpad.app"
mkdir -p "$app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS"
cp "$repo_root/macos/Resources/Info.plist" "$app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :CFBundleShortVersionString 9.8.7' "$app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :CFBundleVersion 42' "$app/Contents/Info.plist"

cat > "$app/Contents/Helpers/scratchpad-mcp" <<'SH'
#!/bin/sh
case "${1:-}" in
  --version) printf 'scratchpad-mcp 9.8.7\n' ;;
  --build-info) printf 'scratchpad-mcp 9.8.7 (build 42)\n' ;;
  *) exit 1 ;;
esac
SH
cat > "$app/Contents/Helpers/scratchpad-service" <<'SH'
#!/bin/sh
test "${1:-}" = --version
printf 'scratchpad-service 9.8.7 (build 42; queue-schema 1)\n'
SH
chmod +x "$app/Contents/Helpers/scratchpad-mcp" "$app/Contents/Helpers/scratchpad-service"
cp "$repo_root/macos/Resources/KeychainHelper-Info.plist" "$app/Contents/Helpers/scratchpad-keychain.app/Contents/Info.plist"
cat > "$app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS/scratchpad-keychain" <<'SH'
#!/bin/sh
exit 1
SH
chmod +x "$app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS/scratchpad-keychain"
"$repo_root/macos/Scripts/validate-bundle.sh" "$app" >/dev/null

cat > "$app/Contents/Helpers/scratchpad-mcp" <<'SH'
#!/bin/sh
printf 'scratchpad-mcp dev (build dev)\n'
SH
chmod +x "$app/Contents/Helpers/scratchpad-mcp"
if "$repo_root/macos/Scripts/validate-bundle.sh" "$app" >/dev/null 2>&1; then
  echo "bundle validator accepted mismatched MCP build provenance" >&2
  exit 1
fi

echo "bundle validator matched helper versions, build number, and service queue schema to app metadata"
