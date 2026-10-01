#!/usr/bin/env bash
set -euo pipefail
: "${RELEASE_TAG:?Set the published vX.Y.Z release}"
[[ "$RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]
root=$(git rev-parse --show-toplevel)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
gh release download "$RELEASE_TAG" --repo alexcatdad/scratchpad --dir "$work/assets"
(cd "$work/assets" && shasum -a 256 --check checksums.txt)
mkdir "$work/typescript" "$work/go"
npm install --prefix "$work/typescript" --ignore-scripts --no-audit --no-fund \
  "$work/assets/scratchpad-api-client-${RELEASE_TAG#v}.tgz"
export SCRATCHPAD_SDK_TS_MODULE="$work/typescript/node_modules/@scratchpad/api-client/dist/index.js"
(
  cd "$work/go"
  go mod init scratchpad-release-consumer
  go get "github.com/alexcatdad/scratchpad/packages/clients/go@${RELEASE_TAG}"
)
# Exercise the existing integration assertions as an external module consumer.
# No replace directive or local generated Go implementation is used.
sed 's/^package scratchpad$/package consumer_test\n\nimport . "github.com\/alexcatdad\/scratchpad\/packages\/clients\/go"/' \
  "$root/packages/clients/go/client_test.go" > "$work/go/client_test.go"
export SCRATCHPAD_SDK_GO_DIRECTORY="$work/go"
cd "$root"
npm run sdk:test
