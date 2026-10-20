#!/bin/sh
set -eu

repo=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/scratchpad-key-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
cat > "$tmp/kubectl" <<'EOF'
#!/bin/sh
set -eu
case "$*" in
  "config current-context") printf '%s\n' test-context ;;
  *" get secret present --ignore-not-found -o json")
    printf '{"metadata":{"name":"present"},"type":"Opaque","data":{"installation-key":"%s"}}\n' "$SYNTHETIC_KEY_B64" ;;
  *" get secret absent --ignore-not-found -o json") printf '{}\n' ;;
  *" create secret generic absent "*) printf 'create\n' >> "$KUBECTL_LOG" ;;
  *) echo "unexpected kubectl invocation: $*" >&2; exit 90 ;;
esac
EOF
chmod +x "$tmp/kubectl"
export PATH="$tmp:$PATH"
export KUBECTL_LOG="$tmp/kubectl.log"
export KUBE_CONTEXT=test-context KUBE_NAMESPACE=test KEY_FILE="$tmp/existing.key"
openssl rand -base64 32 > "$KEY_FILE"
chmod 600 "$KEY_FILE"
SYNTHETIC_KEY_B64=$(base64 < "$KEY_FILE" | tr -d '\n')
export SYNTHETIC_KEY_B64

KEY_SECRET=present "$repo/scripts/adopt-kubernetes-key.sh" --check >/dev/null
if SYNTHETIC_KEY_B64=YWJj KEY_SECRET=present "$repo/scripts/adopt-kubernetes-key.sh" --check >/dev/null 2>&1; then
  echo 'key adoption accepted a different Secret value' >&2
  exit 1
fi
if KEY_SECRET=present "$repo/scripts/adopt-kubernetes-key.sh" --apply >/dev/null 2>&1; then
  echo 'key adoption overwrote an existing Secret' >&2
  exit 1
fi
if KEY_SECRET=absent "$repo/scripts/adopt-kubernetes-key.sh" --apply >/dev/null 2>&1; then
  echo 'key adoption created a Secret without explicit approval' >&2
  exit 1
fi
KEY_SECRET=absent SCRATCHPAD_KEY_ADOPTION_APPROVED=yes "$repo/scripts/adopt-kubernetes-key.sh" --apply >/dev/null
test "$(cat "$KUBECTL_LOG")" = create
