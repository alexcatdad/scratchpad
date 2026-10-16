#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
source_script="$repo_root/macos/Scripts/dev-app.sh"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-dev-app-test.XXXXXX")
test_root=$(CDPATH= cd -- "$test_root" && pwd)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fake_repo="$test_root/repo"
fake_bin="$test_root/bin"
log="$test_root/commands.log"
mkdir -p "$fake_repo/macos/Scripts" "$fake_repo/macos/Resources" "$fake_bin"
cp "$source_script" "$fake_repo/macos/Scripts/dev-app.sh"
chmod +x "$fake_repo/macos/Scripts/dev-app.sh"
cat > "$fake_repo/macos/Resources/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleDevelopmentRegion</key><string>en</string>
<key>CFBundleExecutable</key><string>Scratchpad</string>
<key>CFBundleIdentifier</key><string>com.alexcatdad.scratchpad</string>
<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
<key>CFBundleIconFile</key><string>ScratchpadAppIcon.icns</string>
<key>CFBundleName</key><string>Scratchpad</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/>
<key>SUEnableAutomaticChecks</key><true/>
<key>SUFeedURL</key><string>https://example.invalid/appcast.xml</string>
<key>SUPublicEDKey</key><string>fixture</string>
</dict></plist>
EOF
for resource in ScratchpadAppIcon.icns ScratchpadTrayIcon.png ScratchpadTrayIcon@2x.png; do
  printf 'fixture:%s\n' "$resource" > "$fake_repo/macos/Resources/$resource"
done
printf 'macro plugin\n' > "$test_root/macro.dylib"
mkdir -p "$test_root/Developer/Platforms/MacOSX.platform/Developer/usr/lib/swift/host/plugins"
cp "$test_root/macro.dylib" "$test_root/Developer/Platforms/MacOSX.platform/Developer/usr/lib/swift/host/plugins/libSwiftUIMacros.dylib"

cat > "$fake_bin/swift" <<'EOF'
#!/bin/sh
set -eu
printf 'swift %s\n' "$*" >> "$COMMAND_LOG"
case " $* " in
  *" --show-bin-path "*) printf '%s\n' "$FAKE_BIN_DIR" ;;
  *) mkdir -p "$FAKE_BIN_DIR"; printf '%s\n' "$FAKE_BUILD_CONTENT" > "$FAKE_BIN_DIR/Scratchpad"; chmod +x "$FAKE_BIN_DIR/Scratchpad" ;;
esac
EOF
cat > "$fake_bin/codesign" <<'EOF'
#!/bin/sh
set -eu
printf 'codesign %s\n' "$*" >> "$COMMAND_LOG"
case " $* " in
  *" --verify "*) exit 0 ;;
esac
[ "${CODESIGN_FAIL:-0}" != 1 ] || exit 1
EOF
cat > "$fake_bin/open" <<'EOF'
#!/bin/sh
set -eu
printf 'open %s\n' "$*" >> "$COMMAND_LOG"
[ "${OPEN_FAIL:-0}" != 1 ] || exit 1
EOF
cat > "$fake_bin/go" <<'EOF'
#!/bin/sh
set -eu
printf 'go %s\n' "$*" >> "$COMMAND_LOG"
out=
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then out=$2; shift 2; else shift; fi
done
[ -n "$out" ] || { echo 'fake go build did not receive -o' >&2; exit 2; }
mkdir -p "$(dirname "$out")"
printf '%s\n' "$GO_BUILD_CONTENT" > "$out"
chmod +x "$out"
EOF
cat > "$fake_bin/ps" <<'EOF'
#!/bin/sh
set -eu
printf 'ps %s\n' "$*" >> "$COMMAND_LOG"
printf ' 4242 %s\n' "$DEV_EXECUTABLE"
printf ' 4243 /Applications/Scratchpad.app/Contents/MacOS/Scratchpad\n'
printf ' 4244 /tmp/scratchpad-501/scratchpad-service\n'
EOF
cat > "$fake_bin/kill" <<'EOF'
#!/bin/sh
set -eu
printf 'kill %s\n' "$*" >> "$COMMAND_LOG"
case "$1" in
  -TERM) [ "$2" = 4242 ] ;;
  -0) exit 1 ;;
  *) exit 2 ;;
esac
EOF
cat > "$fake_bin/sleep" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$fake_bin"/*

base_app="$test_root/Scratchpad.app"
mkdir -p \
  "$base_app/Contents/MacOS" \
  "$base_app/Contents/Frameworks/Sparkle.framework" \
  "$base_app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS" \
  "$base_app/Contents/Helpers"
cp "$fake_repo/macos/Resources/Info.plist" "$base_app/Contents/Info.plist"
printf 'base ui\n' > "$base_app/Contents/MacOS/Scratchpad"
printf 'helper\n' > "$base_app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS/scratchpad-keychain"
printf 'service\n' > "$base_app/Contents/Helpers/scratchpad-service"
printf 'mcp\n' > "$base_app/Contents/Helpers/scratchpad-mcp"
"${PLIST_BUDDY_BIN:-/usr/libexec/PlistBuddy}" -c 'Set :CFBundleIdentifier com.alexcatdad.scratchpad' "$base_app/Contents/Info.plist"
mkdir -p "$test_root/Developer/Platforms/MacOSX.platform/Developer/usr/lib/swift/host/plugins"

export PATH="$fake_bin:$PATH"
export COMMAND_LOG="$log"
export FAKE_BIN_DIR="$test_root/swift-bin"
export FAKE_BUILD_CONTENT=first-build
export SWIFT_BIN="$fake_bin/swift"
export GO_BIN="$fake_bin/go"
export CODESIGN_BIN="$fake_bin/codesign"
export OPEN_BIN="$fake_bin/open"
export PS_BIN="$fake_bin/ps"
export KILL_BIN="$fake_bin/kill"
export SLEEP_BIN="$fake_bin/sleep"
export SCRATCHPAD_XCODE_DEVELOPER_DIR="$test_root/Developer"
export PLIST_BUDDY_BIN=/usr/libexec/PlistBuddy

cat > "$fake_repo/macos/Scripts/configure-app-framework-rpath.sh" <<'EOF'
#!/bin/sh
set -eu
printf 'rpath %s\n' "$*" >> "$COMMAND_LOG"
EOF
chmod +x "$fake_repo/macos/Scripts/configure-app-framework-rpath.sh"

if "$fake_repo/macos/Scripts/dev-app.sh" >/dev/null 2>&1; then
  echo 'first build without --base-app unexpectedly succeeded' >&2
  exit 1
fi
: > "$log"
"$fake_repo/macos/Scripts/dev-app.sh" --base-app "$base_app" >/dev/null
dev_app="$fake_repo/macos/.build/dev/Scratchpad Dev.app"
dev_executable="$dev_app/Contents/MacOS/Scratchpad"
[ "$(cat "$dev_executable")" = first-build ] || { echo 'build-only did not copy the compiled executable' >&2; exit 1; }
[ "$("$PLIST_BUDDY_BIN" -c 'Print :CFBundleIdentifier' "$dev_app/Contents/Info.plist")" = com.alexcatdad.scratchpad.dev ] || {
  echo 'dev app bundle identifier was not isolated' >&2
  exit 1
}
! grep -q '^open ' "$log" || { echo 'build-only opened the app' >&2; exit 1; }
! grep -q '^ps ' "$log" || { echo 'build-only queried processes' >&2; exit 1; }
! grep -q '^kill ' "$log" || { echo 'build-only signaled a process' >&2; exit 1; }
[ ! -e "$dev_app/Contents/Info.plist" ] || {
  ! "$PLIST_BUDDY_BIN" -c 'Print :SUFeedURL' "$dev_app/Contents/Info.plist" >/dev/null 2>&1
  ! "$PLIST_BUDDY_BIN" -c 'Print :SUPublicEDKey' "$dev_app/Contents/Info.plist" >/dev/null 2>&1
}
[ -d "$dev_app/Contents/Frameworks/Sparkle.framework" ] || { echo 'nested framework was not preserved' >&2; exit 1; }
[ -f "$dev_app/Contents/Helpers/scratchpad-service" ] || { echo 'service helper was not preserved' >&2; exit 1; }
[ "$(cat "$dev_app/Contents/Helpers/scratchpad-service")" = service ] || { echo 'default UI build replaced the service helper' >&2; exit 1; }
[ "$(cat "$dev_app/Contents/Helpers/scratchpad-mcp")" = mcp ] || { echo 'default UI build replaced the MCP helper' >&2; exit 1; }
! grep -q '^go ' "$log" || { echo 'default UI build invoked Go' >&2; exit 1; }

"$PLIST_BUDDY_BIN" -c 'Set :CFBundleIdentifier com.alexcatdad.scratchpad' "$dev_app/Contents/Info.plist"
: > "$log"
if "$fake_repo/macos/Scripts/dev-app.sh" --base-app "$base_app" >/dev/null 2>&1; then
  echo '--base-app replaced a dev destination with the wrong bundle identifier' >&2
  exit 1
fi
! grep -q '^swift ' "$log" || { echo 'invalid destination was discovered after building' >&2; exit 1; }
"$PLIST_BUDDY_BIN" -c 'Set :CFBundleIdentifier com.alexcatdad.scratchpad.dev' "$dev_app/Contents/Info.plist"

: > "$log"
export GO_BUILD_CONTENT=rebuilt-helper
"$fake_repo/macos/Scripts/dev-app.sh" --rebuild-helpers >/dev/null
[ "$(cat "$dev_app/Contents/Helpers/scratchpad-service")" = rebuilt-helper ] || { echo '--rebuild-helpers did not replace the service helper' >&2; exit 1; }
[ "$(cat "$dev_app/Contents/Helpers/scratchpad-mcp")" = rebuilt-helper ] || { echo '--rebuild-helpers did not replace the MCP helper' >&2; exit 1; }
[ "$(cat "$dev_app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS/scratchpad-keychain")" = helper ] || { echo '--rebuild-helpers changed the Keychain helper' >&2; exit 1; }
[ -d "$dev_app/Contents/Frameworks/Sparkle.framework" ] || { echo '--rebuild-helpers changed Sparkle' >&2; exit 1; }
grep -Fq 'go build -C ' "$log" || { echo '--rebuild-helpers did not build Go helpers' >&2; exit 1; }
grep -Fq -- '-X main.version=dev -X main.buildNumber=dev' "$log" || { echo '--rebuild-helpers did not use development metadata' >&2; exit 1; }
grep -Eq '^codesign --force --sign - --options 0 .*/Contents/Helpers/scratchpad-service$' "$log" || { echo '--rebuild-helpers did not ad-hoc sign the service' >&2; exit 1; }
grep -Eq '^codesign --force --sign - --options 0 .*/Contents/Helpers/scratchpad-mcp$' "$log" || { echo '--rebuild-helpers did not ad-hoc sign MCP' >&2; exit 1; }

export FAKE_BUILD_CONTENT=failed-replacement
if CODESIGN_FAIL=1 "$fake_repo/macos/Scripts/dev-app.sh" >/dev/null 2>&1; then
  echo 'injected codesign failure unexpectedly succeeded' >&2
  exit 1
fi
[ "$(cat "$dev_executable")" = first-build ] || { echo 'failed replacement damaged the previous dev app' >&2; exit 1; }

: > "$log"
export DEV_EXECUTABLE="$dev_executable"
export FAKE_BUILD_CONTENT=second-build
"$fake_repo/macos/Scripts/dev-app.sh" --run >/dev/null
[ "$(cat "$dev_executable")" = second-build ] || { echo '--run did not replace the dev executable' >&2; exit 1; }
grep -qx 'kill -TERM 4242' "$log" || { echo '--run did not terminate only the matching dev process' >&2; exit 1; }
! grep -q '4243\|4244' "$log" || { echo '--run targeted the release app or service' >&2; exit 1; }
grep -Fqx "open -n $dev_app" "$log" || { echo '--run did not open the exact dev bundle' >&2; exit 1; }

export FAKE_BUILD_CONTENT=third-build
if OPEN_FAIL=1 "$fake_repo/macos/Scripts/dev-app.sh" --run >/dev/null 2>&1; then
  echo '--run reported success when Launch Services rejected the dev app' >&2
  exit 1
fi
[ "$(cat "$dev_executable")" = second-build ] || { echo 'open failure did not restore the prior dev bundle' >&2; exit 1; }

echo 'dev-app script tests passed: build-only isolation, safe replacement failure, and exact --run targeting'
