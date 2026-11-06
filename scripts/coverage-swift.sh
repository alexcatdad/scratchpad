#!/bin/sh
# Swift tests with a line-coverage floor on the ScratchpadNative library.
# Usage: coverage-swift.sh <floor-percent>
set -eu

floor=$1
root=$(cd "$(dirname "$0")/.." && pwd)
package="$root/macos"

swift test --package-path "$package" --enable-code-coverage -Xswiftc -warnings-as-errors
bin=$(swift build --package-path "$package" --show-bin-path)
binary="$bin/ScratchpadNativeTests.xctest/Contents/MacOS/ScratchpadNativeTests"
total=$(xcrun llvm-cov report "$binary" -instr-profile "$bin/codecov/default.profdata" \
	"$package/Sources/ScratchpadNative" | awk '$1 == "TOTAL" { gsub(/%/, "", $10); print $10 }')
if [ -z "$total" ]; then
	printf '%s\n' 'Could not read ScratchpadNative line coverage.' >&2
	exit 1
fi
if ! awk -v total="$total" -v floor="$floor" 'BEGIN { exit !(total >= floor) }'; then
	printf 'ScratchpadNative line coverage %s%% is below the %s%% floor.\n' "$total" "$floor" >&2
	exit 1
fi
printf 'ScratchpadNative line coverage: %s%% (%s%% floor)\n' "$total" "$floor"
