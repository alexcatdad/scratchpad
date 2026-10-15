#!/bin/sh
set -eu

app=${1:?usage: validate-release.sh Scratchpad.app appcast.xml}
feed=${2:?usage: validate-release.sh Scratchpad.app appcast.xml}
info="$app/Contents/Info.plist"

test -d "$app" || { echo "missing app bundle" >&2; exit 1; }
test -f "$feed" || { echo "missing Sparkle appcast" >&2; exit 1; }
for key in SUFeedURL SUPublicEDKey; do
  value=$(/usr/libexec/PlistBuddy -c "Print :$key" "$info" 2>/dev/null || true)
  test -n "$value" || { echo "missing $key in bundle metadata" >&2; exit 1; }
done
min=$(/usr/libexec/PlistBuddy -c 'Print :QueueSchemaMinReadable' "$info")
max=$(/usr/libexec/PlistBuddy -c 'Print :QueueSchemaMaxReadable' "$info")
short_version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$info")
build_number=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$info")
case "$min:$max" in *[!0-9:]*|:*|*:) echo "invalid bundle schema range" >&2; exit 1;; esac
test "$min" -gt 0 && test "$max" -ge "$min" || { echo "invalid bundle schema range" >&2; exit 1; }

# Validate the appcast item for this exact bundle version/build. Older releases
# may have different queue-schema ranges as their storage evolves.
/usr/bin/ruby -r rexml/document -e '
  file, short_version, build_number, min, max = ARGV
  doc = REXML::Document.new(File.read(file))
  items = REXML::XPath.match(doc, "//item")
  abort "appcast has no release items" if items.empty?
  candidates = items.select do |item|
    item.elements["sparkle:version"]&.text&.strip == build_number &&
      item.elements["sparkle:shortVersionString"]&.text&.strip == short_version
  end
  abort "appcast has no item for this bundle version and build" if candidates.empty?
  candidates.each do |item|
    low = item.elements["scratchpad:minReadableQueueSchema"]&.text
    high = item.elements["scratchpad:maxReadableQueueSchema"]&.text
    abort "release item is missing queue schema metadata" unless low && high
    abort "release item schema range is invalid" unless low.match?(/\A[1-9][0-9]*\z/) && high.match?(/\A[1-9][0-9]*\z/) && low.to_i <= high.to_i
    abort "candidate schema range does not match bundle metadata" unless low.to_i == min.to_i && high.to_i == max.to_i
  end
' "$feed" "$short_version" "$build_number" "$min" "$max"

echo "The matching version/build appcast candidate matches the bundle schema range. This is a metadata consistency check only; archive signatures, Gatekeeper acceptance, and updater runtime behavior are separate checks."
