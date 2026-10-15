#!/bin/sh
set -eu

binary=${1:?usage: configure-app-framework-rpath.sh executable repository-root developer-directory}
repo_root=${2:?usage: configure-app-framework-rpath.sh executable repository-root developer-directory}
developer_dir=${3:?usage: configure-app-framework-rpath.sh executable repository-root developer-directory}
framework_rpath='@executable_path/../Frameworks'

rpaths=$(otool -l "$binary" | awk '
  /cmd LC_RPATH/ { waiting_for_path = 1; next }
  waiting_for_path && /path / {
    sub(/^[[:space:]]*path /, "")
    sub(/ \(offset [0-9]+\)$/, "")
    print
    waiting_for_path = 0
  }
')

if [ -n "$rpaths" ]; then
  printf '%s\n' "$rpaths" | while IFS= read -r rpath; do
    case "$rpath" in
      "$repo_root"/macos/.build/*|"$developer_dir"/usr/lib/swift-*/macosx)
        install_name_tool -delete_rpath "$rpath" "$binary"
        ;;
    esac
  done
fi

if ! printf '%s\n' "$rpaths" | grep -Fqx "$framework_rpath"; then
  install_name_tool -add_rpath "$framework_rpath" "$binary"
fi
