#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
developer_dir=${DEVELOPER_DIR:-/Library/Developer/CommandLineTools}
export DEVELOPER_DIR="$developer_dir"

swift test --package-path "$repo_root/macos" "$@"
