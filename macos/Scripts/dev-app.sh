#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
package_root="$repo_root/macos"
dev_parent="$package_root/.build/dev"
dev_app="$dev_parent/Scratchpad Dev.app"
dev_executable="$dev_app/Contents/MacOS/Scratchpad"
base_app=
run_app=0
rebuild_helpers=0

usage() {
  cat <<'EOF'
Usage: dev-app.sh [--base-app /path/to/Scratchpad.app] [--rebuild-helpers] [--run]

Build the Scratchpad menu-bar app into macos/.build/dev/Scratchpad Dev.app.
The default only builds and replaces that workspace bundle. --run explicitly
restarts only that dev bundle after the replacement is ready.
On first use, --base-app must point to a complete signed Scratchpad.app bundle.
--rebuild-helpers also builds the current Go MCP and service into the candidate
and ad-hoc signs those two helpers. The default preserves the seed helpers.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --base-app)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      base_app=$2
      shift 2
      ;;
    --run)
      run_app=1
      shift
      ;;
    --rebuild-helpers)
      rebuild_helpers=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

developer_dir=${DEVELOPER_DIR:-/Library/Developer/CommandLineTools}
xcode_developer_dir=${SCRATCHPAD_XCODE_DEVELOPER_DIR:-/Applications/Xcode.app/Contents/Developer}
swiftui_macro="$xcode_developer_dir/Platforms/MacOSX.platform/Developer/usr/lib/swift/host/plugins/libSwiftUIMacros.dylib"
swift_bin=${SWIFT_BIN:-swift}
go_bin=${GO_BIN:-go}
codesign_bin=${CODESIGN_BIN:-/usr/bin/codesign}
ditto_bin=${DITTO_BIN:-/usr/bin/ditto}
open_bin=${OPEN_BIN:-/usr/bin/open}
plist_buddy=${PLIST_BUDDY_BIN:-/usr/libexec/PlistBuddy}
ps_bin=${PS_BIN:-/bin/ps}
awk_bin=${AWK_BIN:-/usr/bin/awk}
kill_bin=${KILL_BIN:-/bin/kill}
sleep_bin=${SLEEP_BIN:-/bin/sleep}

[ -x "$swift_bin" ] || command -v "$swift_bin" >/dev/null 2>&1 || { echo "Swift was not found" >&2; exit 1; }
[ -f "$swiftui_macro" ] || { echo "SwiftUI macro plugin not found: $swiftui_macro" >&2; exit 1; }
[ -x "$codesign_bin" ] || { echo "codesign was not found: $codesign_bin" >&2; exit 1; }
[ -x "$ditto_bin" ] || { echo "ditto was not found: $ditto_bin" >&2; exit 1; }
[ -x "$plist_buddy" ] || { echo "PlistBuddy was not found: $plist_buddy" >&2; exit 1; }
[ "$rebuild_helpers" -eq 0 ] || { [ -x "$go_bin" ] || command -v "$go_bin" >/dev/null 2>&1; } || {
  echo "Go was not found" >&2
  exit 1
}

export DEVELOPER_DIR="$developer_dir"
mkdir -p "$package_root/.build"
mkdir -p /private/tmp/scratchpad-dev-clang-module-cache /private/tmp/scratchpad-dev-swift-module-cache
export CLANG_MODULE_CACHE_PATH=/private/tmp/scratchpad-dev-clang-module-cache
export SWIFTPM_MODULECACHE_OVERRIDE=/private/tmp/scratchpad-dev-swift-module-cache

dev_app_exists=0
if [ -e "$dev_app" ] || [ -L "$dev_app" ]; then
  [ ! -L "$dev_app" ] && [ -d "$dev_app" ] && [ -f "$dev_app/Contents/Info.plist" ] || {
    echo "refusing to replace an invalid dev app bundle: $dev_app" >&2
    exit 1
  }
  existing_id=$("$plist_buddy" -c 'Print :CFBundleIdentifier' "$dev_app/Contents/Info.plist")
  [ "$existing_id" = "com.alexcatdad.scratchpad.dev" ] || {
    echo "refusing to replace a bundle with identifier $existing_id at $dev_app" >&2
    exit 1
  }
  dev_app_exists=1
fi

if [ -n "$base_app" ]; then
  seed_app=$base_app
elif [ "$dev_app_exists" -eq 1 ]; then
  seed_app=$dev_app
else
  echo "first build requires --base-app /path/to/Scratchpad.app" >&2
  exit 1
fi

[ -d "$seed_app" ] && [ -f "$seed_app/Contents/Info.plist" ] || { echo "base app bundle not found: $seed_app" >&2; exit 1; }
seed_id=$("$plist_buddy" -c 'Print :CFBundleIdentifier' "$seed_app/Contents/Info.plist")
case "$seed_id" in
  com.alexcatdad.scratchpad|com.alexcatdad.scratchpad.dev) ;;
  *) echo "base app has unexpected bundle identifier: $seed_id" >&2; exit 1 ;;
esac
for required in \
  "$seed_app/Contents/Helpers/scratchpad-service" \
  "$seed_app/Contents/Helpers/scratchpad-mcp" \
  "$seed_app/Contents/Helpers/scratchpad-keychain.app/Contents/MacOS/scratchpad-keychain"; do
  [ -e "$required" ] || { echo "base app is missing required bundled code: $required" >&2; exit 1; }
done
"$codesign_bin" --verify --deep --strict "$seed_app"

"$swift_bin" build --package-path "$package_root" --product Scratchpad \
  -Xswiftc -load-plugin-library -Xswiftc "$swiftui_macro"
bin_dir=$("$swift_bin" build --package-path "$package_root" --show-bin-path)
source_executable="$bin_dir/Scratchpad"
[ -x "$source_executable" ] || { echo "built Scratchpad executable is missing: $source_executable" >&2; exit 1; }

mkdir -p "$dev_parent"
stage_root=$(mktemp -d "$dev_parent/.scratchpad-dev.XXXXXX")
candidate="$stage_root/Scratchpad Dev.app"
backup="$stage_root/previous.app"
backup_moved=0
cleanup() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ "$backup_moved" -eq 1 ] && [ -e "$backup" ]; then
    if [ -e "$dev_app" ]; then rm -rf "$dev_app"; fi
    if ! mv "$backup" "$dev_app"; then
      echo "failed to restore previous dev app; backup retained at $backup" >&2
      exit 1
    fi
  fi
  rm -rf "$stage_root"
  exit "$result"
}
trap cleanup EXIT HUP INT TERM

"$ditto_bin" "$seed_app" "$candidate"
# Remove the retired updater when reusing an older dev bundle as the seed.
rm -rf "$candidate/Contents/Frameworks/Sparkle.framework"
mkdir -p "$candidate/Contents/MacOS" "$candidate/Contents/Resources"
cp "$source_executable" "$candidate/Contents/MacOS/Scratchpad"
cp "$repo_root/macos/Resources/ScratchpadAppIcon.icns" "$candidate/Contents/Resources/ScratchpadAppIcon.icns"
cp "$repo_root/macos/Resources/ScratchpadTrayIcon.png" "$candidate/Contents/Resources/ScratchpadTrayIcon.png"
cp "$repo_root/macos/Resources/ScratchpadTrayIcon@2x.png" "$candidate/Contents/Resources/ScratchpadTrayIcon@2x.png"
cp "$repo_root/macos/Resources/Info.plist" "$candidate/Contents/Info.plist"
sh "$repo_root/macos/Scripts/configure-app-framework-rpath.sh" \
  "$candidate/Contents/MacOS/Scratchpad" "$repo_root" "$developer_dir"
"$plist_buddy" -c 'Set :CFBundleIdentifier com.alexcatdad.scratchpad.dev' "$candidate/Contents/Info.plist"
"$plist_buddy" -c 'Set :CFBundleName Scratchpad Dev' "$candidate/Contents/Info.plist"
"$plist_buddy" -c 'Set :CFBundleDisplayName Scratchpad Dev' "$candidate/Contents/Info.plist" 2>/dev/null || \
  "$plist_buddy" -c 'Add :CFBundleDisplayName string Scratchpad Dev' "$candidate/Contents/Info.plist"
plutil -lint "$candidate/Contents/Info.plist" >/dev/null

# Rebuild the Go helpers only when explicitly requested. Keep the default UI
# iteration fast and preserve all signed seed components.
if [ "$rebuild_helpers" -eq 1 ]; then
  "$go_bin" build -C "$repo_root/mcp" -ldflags '-X main.version=dev -X main.buildNumber=dev' \
    -o "$candidate/Contents/Helpers/scratchpad-service" ./cmd/scratchpad-service
  "$go_bin" build -C "$repo_root/mcp" -ldflags '-X main.version=dev -X main.buildNumber=dev' \
    -o "$candidate/Contents/Helpers/scratchpad-mcp" ./cmd/scratchpad-mcp
  "$codesign_bin" --force --sign - --options 0 "$candidate/Contents/Helpers/scratchpad-service"
  "$codesign_bin" --force --sign - --options 0 "$candidate/Contents/Helpers/scratchpad-mcp"
fi

# Only rebuilt components receive local ad-hoc signatures, with no hardened
# runtime options. The unchanged signed Keychain helper remains from the seed.
"$codesign_bin" --force --sign - --options 0 "$candidate/Contents/MacOS/Scratchpad"
"$codesign_bin" --force --sign - --options 0 "$candidate"
"$codesign_bin" --verify --deep --strict "$candidate"

if [ "$run_app" -eq 1 ] && [ -e "$dev_app" ]; then
  # This process query is intentionally confined to --run. Match the complete
  # executable path before sending TERM; never target a release app or service.
  running_pids=$("$ps_bin" -ww -axo pid=,comm= | "$awk_bin" -v target="$dev_executable" '
    {
      line = $0
      pid = line
      sub(/^[[:space:]]*/, "", pid)
      sub(/[[:space:]].*$/, "", pid)
      sub(/^[[:space:]]*[0-9]+[[:space:]]+/, "", line)
      command = line
      if (command == target) print pid
    }')
  if [ -n "$running_pids" ]; then
    for pid in $running_pids; do "$kill_bin" -TERM "$pid"; done
    attempt=0
    while [ "$attempt" -lt 50 ]; do
      still_running=0
      for pid in $running_pids; do
        if "$kill_bin" -0 "$pid" 2>/dev/null; then still_running=1; fi
      done
      [ "$still_running" -eq 1 ] || break
      attempt=$((attempt + 1))
      "$sleep_bin" 0.1
    done
    [ "$still_running" -eq 0 ] || { echo "dev app did not close after TERM; close it manually and retry" >&2; exit 1; }
  fi
fi

if [ -e "$dev_app" ]; then
  backup_moved=1
  if ! mv "$dev_app" "$backup"; then
    backup_moved=0
    echo "could not move the previous dev app out of the way" >&2
    exit 1
  fi
fi
if ! mv "$candidate" "$dev_app"; then
  if [ "$backup_moved" -eq 1 ]; then mv "$backup" "$dev_app"; backup_moved=0; fi
  echo "could not replace the workspace dev app at $dev_app" >&2
  exit 1
fi
if [ "$rebuild_helpers" -eq 1 ]; then
  echo "Built $dev_app (UI and Go helpers ad-hoc signed; Keychain helper preserved)."
else
  echo "Built $dev_app (ad-hoc local signature; nested signed components preserved)."
fi
if [ "$run_app" -eq 1 ]; then
  if ! "$open_bin" -n "$dev_app"; then
    rm -rf "$dev_app"
    echo "could not open the dev app; restoring the previous workspace bundle if present" >&2
    exit 1
  fi
fi
backup_moved=0
