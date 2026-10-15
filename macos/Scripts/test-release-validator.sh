#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-release-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
app="$tmp/Scratchpad.app"
mkdir -p "$app/Contents"
cp "$repo_root/macos/Resources/Info.plist" "$app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Add :SUFeedURL string https://updates.example.test/appcast.xml' "$app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Add :SUPublicEDKey string synthetic-public-verification-key' "$app/Contents/Info.plist"

cat > "$tmp/valid.xml" <<'XML'
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle" xmlns:scratchpad="https://scratchpad.alexcatdad.com/xml-namespaces/tray-update">
  <channel>
    <item><sparkle:version>2</sparkle:version><sparkle:shortVersionString>2.0.0</sparkle:shortVersionString><scratchpad:minReadableQueueSchema>2</scratchpad:minReadableQueueSchema><scratchpad:maxReadableQueueSchema>2</scratchpad:maxReadableQueueSchema></item>
    <item><sparkle:version>1</sparkle:version><sparkle:shortVersionString>1.0.0</sparkle:shortVersionString><scratchpad:minReadableQueueSchema>1</scratchpad:minReadableQueueSchema><scratchpad:maxReadableQueueSchema>1</scratchpad:maxReadableQueueSchema></item>
  </channel>
</rss>
XML
"$repo_root/macos/Scripts/validate-release.sh" "$app" "$tmp/valid.xml" >/dev/null

cat > "$tmp/missing.xml" <<'XML'
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle" xmlns:scratchpad="https://scratchpad.alexcatdad.com/xml-namespaces/tray-update"><channel><item><sparkle:version>1</sparkle:version><sparkle:shortVersionString>1.0.0</sparkle:shortVersionString><scratchpad:minReadableQueueSchema>1</scratchpad:minReadableQueueSchema></item></channel></rss>
XML
if "$repo_root/macos/Scripts/validate-release.sh" "$app" "$tmp/missing.xml" >/dev/null 2>&1; then
  echo "validator accepted a candidate with missing schema metadata" >&2
  exit 1
fi

cat > "$tmp/incompatible.xml" <<'XML'
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle" xmlns:scratchpad="https://scratchpad.alexcatdad.com/xml-namespaces/tray-update"><channel><item><sparkle:version>1</sparkle:version><sparkle:shortVersionString>1.0.0</sparkle:shortVersionString><scratchpad:minReadableQueueSchema>2</scratchpad:minReadableQueueSchema><scratchpad:maxReadableQueueSchema>2</scratchpad:maxReadableQueueSchema></item></channel></rss>
XML
if "$repo_root/macos/Scripts/validate-release.sh" "$app" "$tmp/incompatible.xml" >/dev/null 2>&1; then
  echo "validator accepted candidate metadata that disagrees with bundle schema" >&2
  exit 1
fi

cat > "$tmp/no-candidate.xml" <<'XML'
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle" xmlns:scratchpad="https://scratchpad.alexcatdad.com/xml-namespaces/tray-update"><channel><item><sparkle:version>2</sparkle:version><sparkle:shortVersionString>2.0.0</sparkle:shortVersionString><scratchpad:minReadableQueueSchema>1</scratchpad:minReadableQueueSchema><scratchpad:maxReadableQueueSchema>1</scratchpad:maxReadableQueueSchema></item></channel></rss>
XML
if "$repo_root/macos/Scripts/validate-release.sh" "$app" "$tmp/no-candidate.xml" >/dev/null 2>&1; then
  echo "validator accepted a feed without the bundle's candidate version/build" >&2
  exit 1
fi

echo "release validator checked the matching candidate, allowed older schema history, and rejected missing/incompatible candidate metadata"
