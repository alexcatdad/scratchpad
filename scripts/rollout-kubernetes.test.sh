#!/bin/sh
set -eu

repo=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-rollout-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
cat > "$tmp/kubectl" <<'EOF'
#!/bin/sh
set -eu
case "$*" in
  "config current-context") printf '%s\n' test-context ;;
  *" get deployment scratchpad -o json")
    cat <<'JSON'
{"spec":{"template":{"spec":{"containers":[{"name":"scratchpad","image":"ghcr.io/alexcatdad/scratchpad@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","command":["npm","run","start"],"args":["-w","@scratchpad/web"],"env":[{"name":"SCRATCHPAD_ENCRYPTION_KEY_FILE","value":"/run/secrets/scratchpad/installation-key"}],"volumeMounts":[{"name":"encryption-key","mountPath":"/run/secrets/scratchpad","readOnly":true},{"name":"data","mountPath":"/data"}]}],"volumes":[{"name":"encryption-key","secret":{"secretName":"existing-key"}},{"name":"data","persistentVolumeClaim":{"claimName":"existing-data"}}]}}}}
JSON
    ;;
  *" set image deployment/scratchpad scratchpad="*) printf 'set-image\n' >> "$KUBECTL_LOG" ;;
  *" patch deployment/scratchpad --type=json -p "*) printf 'patch %s\n' "$*" >> "$KUBECTL_LOG" ;;
  *" rollout status deployment/scratchpad --timeout=10m") printf 'rollout\n' >> "$KUBECTL_LOG" ;;
  *) echo "unexpected kubectl invocation: $*" >&2; exit 90 ;;
esac
EOF
chmod +x "$tmp/kubectl"
export PATH="$tmp:$PATH"
export KUBECTL_LOG="$tmp/kubectl.log"
export KUBE_CONTEXT=test-context KUBE_NAMESPACE=test KUBE_DEPLOYMENT=scratchpad KUBE_CONTAINER=scratchpad
export EXPECTED_CURRENT_IMAGE="ghcr.io/alexcatdad/scratchpad@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
export SCRATCHPAD_IMAGE="ghcr.io/alexcatdad/scratchpad@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
export EXPECTED_KEY_SECRET=existing-key EXPECTED_KEY_ITEM=installation-key EXPECTED_KEY_PATH=/run/secrets/scratchpad/installation-key EXPECTED_PVC=existing-data
export SCRATCHPAD_BACKUP_REF=test-backup SCRATCHPAD_BACKUP_VERIFIED=yes SCRATCHPAD_MIGRATION_STATE=complete

"$repo/scripts/rollout-kubernetes.sh" --check >/dev/null
if EXPECTED_KEY_SECRET=wrong-key "$repo/scripts/rollout-kubernetes.sh" --check >/dev/null 2>&1; then
  echo 'rollout accepted an unexpected key Secret' >&2
  exit 1
fi
if EXPECTED_PVC=wrong-data "$repo/scripts/rollout-kubernetes.sh" --check >/dev/null 2>&1; then
  echo 'rollout accepted an unexpected PVC' >&2
  exit 1
fi
if SCRATCHPAD_IMAGE=ghcr.io/alexcatdad/scratchpad:latest "$repo/scripts/rollout-kubernetes.sh" --check >/dev/null 2>&1; then
  echo 'rollout accepted a mutable image tag' >&2
  exit 1
fi
if "$repo/scripts/rollout-kubernetes.sh" --apply >/dev/null 2>&1; then
  echo 'rollout applied without explicit approval' >&2
  exit 1
fi
if SCRATCHPAD_BACKUP_VERIFIED=no SCRATCHPAD_CHANGE_APPROVED=yes "$repo/scripts/rollout-kubernetes.sh" --apply >/dev/null 2>&1; then
  echo 'rollout proceeded without a verified backup' >&2
  exit 1
fi
if SCRATCHPAD_MIGRATION_STATE=pending SCRATCHPAD_CHANGE_APPROVED=yes "$repo/scripts/rollout-kubernetes.sh" --apply >/dev/null 2>&1; then
  echo 'rollout proceeded before migration completion' >&2
  exit 1
fi
SCRATCHPAD_CHANGE_APPROVED=yes "$repo/scripts/rollout-kubernetes.sh" --apply >/dev/null
if "$repo/scripts/rollout-kubernetes.sh" --switch-to-go >/dev/null 2>&1; then
  echo 'runtime switch proceeded without separate approval' >&2
  exit 1
fi
SCRATCHPAD_RUNTIME_SWITCH_APPROVED=yes "$repo/scripts/rollout-kubernetes.sh" --switch-to-go >/dev/null
test "$(cat "$KUBECTL_LOG" | sed -n '1p;2p')" = "$(printf 'set-image\nrollout')"
grep -q '"op":"remove"' "$KUBECTL_LOG"
