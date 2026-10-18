#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-release-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
mkdir -p "$tmp/bin" "$tmp/repo/dist/release"

cat > "$tmp/bin/uname" <<'SH'
#!/bin/sh
case "$1" in
  -s) printf 'Darwin\n' ;;
  -m) printf 'arm64\n' ;;
  *) exit 2 ;;
esac
SH
cat > "$tmp/bin/git" <<SH
#!/bin/sh
case "\$*" in
  'rev-parse --show-toplevel') printf '%s\\n' '$tmp/repo' ;;
  'status --porcelain') ;;
  'rev-parse HEAD') printf 'synthetic-commit\\n' ;;
  'rev-parse refs/tags/v1.2.3^{commit}') printf 'synthetic-commit\\n' ;;
  *) echo "unexpected git call: \$*" >&2; exit 2 ;;
esac
SH
chmod +x "$tmp/bin/uname" "$tmp/bin/git"

archive="$tmp/repo/dist/release/scratchpad-v1.2.3-darwin-arm64.zip"
receipt="$tmp/repo/dist/release/notarization-app-v1.2.3.json"
manifest="$tmp/repo/dist/release/macos-app-release.json"
for output in "$archive" "$receipt" "$manifest"; do
  rm -f "$archive" "$receipt" "$manifest"
  printf 'previous release evidence\n' > "$output"
  if PATH="$tmp/bin:$PATH" \
    RELEASE_TAG=v1.2.3 \
    APPLE_SIGNING_IDENTITY='Developer ID Application: Synthetic (TEAMID)' \
    APPLE_TEAM_ID=TEAMID \
    KEYCHAIN_PROFILE=/synthetic/profile \
    KEYCHAIN_ACCESS_GROUP=TEAMID.synthetic \
    NOTARYTOOL_PROFILE=synthetic \
    bash "$repo_root/scripts/release-macos-app.sh" >"$tmp/output" 2>&1; then
    echo "release script accepted pre-existing output artifact: $output" >&2
    exit 1
  fi
  grep -q 'Refusing to overwrite existing release artifact' "$tmp/output"
  test "$(cat "$output")" = 'previous release evidence'
  if find "$tmp/repo/dist/release" -name '.scratchpad-release.*' | grep -q .; then
    echo "release preflight created staging output before refusing collision" >&2
    exit 1
  fi
done
echo 'release script refuses existing artifacts before signing and preserves their contents'
