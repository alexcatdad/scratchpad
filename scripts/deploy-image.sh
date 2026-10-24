#!/usr/bin/env bash
# Build and push the go-runtime application image to the container registry,
# run locally by `task deploy`. Publishing an image does not deploy it to a
# cluster (see docs/runbooks/homelab-deploy.md).
#
# Local prerequisites (never committed):
#   - an authenticated registry login for the image host, for example
#     `docker login ghcr.io` with a token that can write packages
#   - a buildx builder that can build and push linux/amd64 and linux/arm64
#     with provenance and SBOM attestations (docker-container driver)
# Optional: SCRATCHPAD_IMAGE overrides the repository (default below).
set -euo pipefail

: "${RELEASE_COMMIT:?Set RELEASE_COMMIT to the published release commit}"
[[ "$RELEASE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
image=${SCRATCHPAD_IMAGE:-ghcr.io/alexcatdad/scratchpad}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-deploy.XXXXXX")
trap 'rm -rf "$work"' EXIT

docker buildx build "$root" \
  --file "$root/Dockerfile" \
  --target go-runtime \
  --platform linux/amd64,linux/arm64 \
  --tag "$image:sha-$RELEASE_COMMIT" \
  --label "org.opencontainers.image.source=https://github.com/alexcatdad/scratchpad" \
  --label "org.opencontainers.image.revision=$RELEASE_COMMIT" \
  --provenance=true --sbom=true \
  --metadata-file "$work/metadata.json" \
  --push
digest=$(jq -er '."containerimage.digest"' "$work/metadata.json")
[[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]
printf 'Published %s@%s from %s\n' "$image" "$digest" "$RELEASE_COMMIT"
