#!/usr/bin/env bash
set -euo pipefail

# CI installs the exact versions below. Local runs fail explicitly on drift.
[[ $(actionlint -version | head -n 1) == '1.7.12' ]]
shellcheck --version | grep -Fqx 'version: 0.11.0'
actionlint
shellcheck scripts/*.sh
# Debian package snapshots are not pinned; exact package revisions disappear
# from ordinary Debian mirrors. All other Dockerfile checks remain enabled.
docker run --rm -i hadolint/hadolint:v2.15.1 hadolint --ignore DL3008 - < Dockerfile
