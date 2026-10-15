#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-bundle-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
app="$tmp/Scratchpad.app"
mkdir -p "$app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS" "$app/Contents/Resources"
cp "$repo_root/macos/Resources/Info.plist" "$app/Contents/Info.plist"
cp "$repo_root/macos/Resources/ScratchpadAppIcon.icns" "$app/Contents/Resources/ScratchpadAppIcon.icns"
cp "$repo_root/macos/Resources/ScratchpadTrayIcon.png" "$app/Contents/Resources/ScratchpadTrayIcon.png"
cp "$repo_root/macos/Resources/ScratchpadTrayIcon@2x.png" "$app/Contents/Resources/ScratchpadTrayIcon@2x.png"
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

mv "$app/Contents/Resources/ScratchpadAppIcon.icns" "$tmp/missing-icon.icns"
if "$repo_root/macos/Scripts/validate-bundle.sh" "$app" >"$tmp/icon-error" 2>&1; then
  echo "bundle validator accepted a missing app icon resource" >&2
  exit 1
fi
rg -q "missing app icon resource" "$tmp/icon-error"
mv "$tmp/missing-icon.icns" "$app/Contents/Resources/ScratchpadAppIcon.icns"
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

echo "bundle validator matched helper versions, build number, queue schema, and icon resources to app metadata"
