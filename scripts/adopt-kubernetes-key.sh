#!/bin/sh
set -eu

usage() {
  cat >&2 <<'EOF'
Usage: adopt-kubernetes-key.sh [--check|--apply]

Required environment: KUBE_CONTEXT, KUBE_NAMESPACE, KEY_SECRET, KEY_FILE.
--check verifies the existing Secret matches KEY_FILE without printing either.
--apply creates the Secret from the supplied existing protected key file only
when that Secret is absent. It refuses to overwrite or rotate an existing key.
EOF
  exit 2
}

mode=${1:---check}
[ "$#" -le 1 ] || usage
case "$mode" in --check|--apply) ;; *) usage ;; esac
for name in KUBE_CONTEXT KUBE_NAMESPACE KEY_SECRET KEY_FILE; do
  case "$name" in
    KUBE_CONTEXT) value=${KUBE_CONTEXT-} ;;
    KUBE_NAMESPACE) value=${KUBE_NAMESPACE-} ;;
    KEY_SECRET) value=${KEY_SECRET-} ;;
    KEY_FILE) value=${KEY_FILE-} ;;
  esac
  [ -n "$value" ] || { echo "Missing required variable: $name" >&2; exit 2; }
done
command -v kubectl >/dev/null 2>&1 || { echo 'kubectl is required.' >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo 'jq is required.' >&2; exit 2; }
actual_context=$(kubectl config current-context)
[ "$actual_context" = "$KUBE_CONTEXT" ] || { echo 'Current Kubernetes context does not match KUBE_CONTEXT.' >&2; exit 1; }
[ -f "$KEY_FILE" ] && [ ! -L "$KEY_FILE" ] || { echo 'KEY_FILE must be a regular, non-symlink file.' >&2; exit 1; }
case "$(uname -s)" in
  Darwin) mode_bits=$(stat -f '%Lp' "$KEY_FILE") ;;
  *) mode_bits=$(stat -c '%a' "$KEY_FILE") ;;
esac
case "$mode_bits" in 400|440|600) ;; *) echo 'KEY_FILE must have mode 0400, 0440 or 0600.' >&2; exit 1 ;; esac
command -v openssl >/dev/null 2>&1 || { echo 'openssl is required.' >&2; exit 2; }
tmp=$(mktemp "${TMPDIR:-/tmp}/scratchpad-key-check.XXXXXX")
trap 'rm -f "$tmp"' EXIT HUP INT TERM
chmod 600 "$tmp"
openssl base64 -d -in "$KEY_FILE" -out "$tmp" 2>/dev/null || { echo 'KEY_FILE is not valid base64 key material.' >&2; exit 1; }
[ "$(wc -c < "$tmp" | tr -d ' ')" = 32 ] || { echo 'KEY_FILE must decode to exactly 32 bytes.' >&2; exit 1; }
expected=$(base64 < "$KEY_FILE" | tr -d '\n')
existing=$(kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" get secret "$KEY_SECRET" --ignore-not-found -o json)
if printf '%s' "$existing" | jq -e '.metadata.name != null' >/dev/null; then
  printf '%s' "$existing" | jq -e '.type == "Opaque" and (.data["installation-key"] | type == "string" and length > 0)' >/dev/null || {
    echo 'The existing Secret is missing the expected key; it was not changed.' >&2
    exit 1
  }
  actual=$(printf '%s' "$existing" | jq -er '.data["installation-key"]')
  [ "$actual" = "$expected" ] || { echo 'Existing Secret does not match the supplied retained key file; it was not changed.' >&2; exit 1; }
  echo 'Existing installation-key Secret matches the supplied key file; its data was not changed.'
  [ "$mode" = --check ] && exit 0
  echo 'Refusing to replace an existing installation-key Secret.' >&2
  exit 1
fi

[ "$mode" = --apply ] || { echo 'Installation-key Secret is absent; no change made.' >&2; exit 1; }
[ "${SCRATCHPAD_KEY_ADOPTION_APPROVED:-}" = yes ] || { echo 'Set SCRATCHPAD_KEY_ADOPTION_APPROVED=yes after owner approval.' >&2; exit 2; }
rm -f "$tmp"
trap - EXIT HUP INT TERM
kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" create secret generic "$KEY_SECRET" \
  --from-file=installation-key="$KEY_FILE" >/dev/null
echo 'Created installation-key Secret from the supplied existing key file.'
