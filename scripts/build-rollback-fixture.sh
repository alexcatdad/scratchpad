#!/bin/sh
set -eu

revision=0990411697f9eda7c535acf0d2f0697d7ae6f149
image=${1:-scratchpad-retained:ci}
context=${TEST_DOCKER_CONTEXT:-default}
root=$(git rev-parse --show-toplevel)

if ! git -C "$root" cat-file -e "$revision^{commit}" 2>/dev/null; then
	git -C "$root" fetch --no-tags origin "$revision"
fi
git -C "$root" cat-file -e "$revision^{commit}"

archive_dir=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-rollback-image.XXXXXX")
cleanup() {
	rm -rf "$archive_dir"
}
trap cleanup EXIT HUP INT TERM

git -C "$root" archive --format=tar "$revision" > "$archive_dir/source.tar"
tar -xf "$archive_dir/source.tar" -C "$archive_dir"
rm "$archive_dir/source.tar"
docker --context "$context" build -t "$image" "$archive_dir"
