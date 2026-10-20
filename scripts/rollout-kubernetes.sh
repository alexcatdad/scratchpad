#!/bin/sh
set -eu

usage() {
  cat >&2 <<'EOF'
Usage: rollout-kubernetes.sh [--check|--apply|--switch-to-go]

Required environment:
  KUBE_CONTEXT, KUBE_NAMESPACE, KUBE_DEPLOYMENT, KUBE_CONTAINER
  EXPECTED_CURRENT_IMAGE, SCRATCHPAD_IMAGE (ghcr.io/...@sha256:...)
  EXPECTED_KEY_SECRET, EXPECTED_KEY_ITEM, EXPECTED_KEY_PATH, EXPECTED_PVC
  SCRATCHPAD_BACKUP_REF, SCRATCHPAD_BACKUP_VERIFIED=yes
  SCRATCHPAD_MIGRATION_STATE=complete

--check is read-only. --apply changes only the selected container image.
--switch-to-go changes the image and clears that container's command/args so
the go-runtime image entrypoint is used. Both mutations require explicit gates.
EOF
  exit 2
}

mode=${1:---check}
[ "$#" -le 1 ] || usage
case "$mode" in --check|--apply|--switch-to-go) ;; *) usage ;; esac
for name in KUBE_CONTEXT KUBE_NAMESPACE KUBE_DEPLOYMENT KUBE_CONTAINER \
  EXPECTED_CURRENT_IMAGE SCRATCHPAD_IMAGE EXPECTED_KEY_SECRET EXPECTED_KEY_ITEM EXPECTED_KEY_PATH EXPECTED_PVC; do
  case "$name" in
    KUBE_CONTEXT) value=${KUBE_CONTEXT-} ;;
    KUBE_NAMESPACE) value=${KUBE_NAMESPACE-} ;;
    KUBE_DEPLOYMENT) value=${KUBE_DEPLOYMENT-} ;;
    KUBE_CONTAINER) value=${KUBE_CONTAINER-} ;;
    EXPECTED_CURRENT_IMAGE) value=${EXPECTED_CURRENT_IMAGE-} ;;
    SCRATCHPAD_IMAGE) value=${SCRATCHPAD_IMAGE-} ;;
    EXPECTED_KEY_SECRET) value=${EXPECTED_KEY_SECRET-} ;;
    EXPECTED_KEY_ITEM) value=${EXPECTED_KEY_ITEM-} ;;
    EXPECTED_KEY_PATH) value=${EXPECTED_KEY_PATH-} ;;
    EXPECTED_PVC) value=${EXPECTED_PVC-} ;;
  esac
  [ -n "$value" ] || { echo "Missing required variable: $name" >&2; exit 2; }
done
case "$SCRATCHPAD_IMAGE" in
  ghcr.io/alexcatdad/scratchpad@sha256:*) ;;
  *) echo 'SCRATCHPAD_IMAGE must be a digest-pinned Scratchpad GHCR image.' >&2; exit 2 ;;
esac
digest=${SCRATCHPAD_IMAGE##*@sha256:}
case "$digest" in *[!0-9a-f]*|'') echo 'SCRATCHPAD_IMAGE has an invalid sha256 digest.' >&2; exit 2 ;; esac
[ "${#digest}" -eq 64 ] || { echo 'SCRATCHPAD_IMAGE has an invalid sha256 digest.' >&2; exit 2; }

command -v kubectl >/dev/null 2>&1 || { echo 'kubectl is required.' >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo 'jq is required.' >&2; exit 2; }
actual_context=$(kubectl config current-context)
[ "$actual_context" = "$KUBE_CONTEXT" ] || { echo 'Current Kubernetes context does not match KUBE_CONTEXT.' >&2; exit 1; }
deployment=$(kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" get deployment "$KUBE_DEPLOYMENT" -o json)
current_image=$(printf '%s' "$deployment" | jq -er --arg name "$KUBE_CONTAINER" '.spec.template.spec.containers[] | select(.name == $name) | .image')
[ "$current_image" = "$EXPECTED_CURRENT_IMAGE" ] || { echo 'Current image changed since the reviewed plan.' >&2; exit 1; }
key_filename=${EXPECTED_KEY_PATH##*/}
key_volume=$(printf '%s' "$deployment" | jq -er --arg name "$KUBE_CONTAINER" --arg mount "${EXPECTED_KEY_PATH%/*}" '.spec.template.spec as $pod | [$pod.containers[] | select(.name == $name) | .volumeMounts[] | select(.mountPath == $mount and .readOnly == true) | .name] as $mounts | $pod.volumes[] | select(.name as $volume | $mounts | index($volume) != null) | .name')
key_secret=$(printf '%s' "$deployment" | jq -er --arg volume "$key_volume" '.spec.template.spec.volumes[] | select(.name == $volume) | .secret.secretName')
[ "$key_secret" = "$EXPECTED_KEY_SECRET" ] || { echo 'Installation-key file mount does not reference the expected existing Secret.' >&2; exit 1; }
key_projection=$(printf '%s' "$deployment" | jq -er --arg volume "$key_volume" --arg item "$EXPECTED_KEY_ITEM" --arg path "$key_filename" '.spec.template.spec.volumes[] | select(.name == $volume) | .secret.items // [] | if length == 0 then ($item == $path) else any(.[]; .key == $item and .path == $path) end')
[ "$key_projection" = true ] || { echo 'Installation-key Secret projection does not map the reviewed key item to the expected filename.' >&2; exit 1; }
key_path=$(printf '%s' "$deployment" | jq -er --arg name "$KUBE_CONTAINER" '.spec.template.spec.containers[] | select(.name == $name) | .env[] | select(.name == "SCRATCHPAD_ENCRYPTION_KEY_FILE") | .value')
[ "$key_path" = "$EXPECTED_KEY_PATH" ] || { echo 'Deployment installation-key file path is not the reviewed path.' >&2; exit 1; }
key_mount=$(printf '%s' "$deployment" | jq -er --arg name "$KUBE_CONTAINER" --arg volume "$key_volume" '.spec.template.spec.containers[] | select(.name == $name) | .volumeMounts[] | select(.name == $volume and .readOnly == true) | .mountPath')
[ "$key_mount" = "${EXPECTED_KEY_PATH%/*}" ] || { echo 'Installation-key Secret is not mounted read-only at the reviewed file path.' >&2; exit 1; }
pvc=$(printf '%s' "$deployment" | jq -er --arg claim "$EXPECTED_PVC" '.spec.template.spec.volumes[] | select(.persistentVolumeClaim.claimName == $claim) | .persistentVolumeClaim.claimName')
[ "$pvc" = "$EXPECTED_PVC" ] || { echo 'Deployment does not reference the expected existing PVC.' >&2; exit 1; }
data_mount=$(printf '%s' "$deployment" | jq -er --arg name "$KUBE_CONTAINER" --arg claim "$EXPECTED_PVC" '[.spec.template.spec.volumes[] | select(.persistentVolumeClaim.claimName == $claim) | .name] as $volumes | .spec.template.spec.containers[] | select(.name == $name) | .volumeMounts[] | select(.name as $volume | $volumes | index($volume) != null) | .mountPath')
[ "$data_mount" = /data ] || { echo 'Expected PVC is not mounted at /data in the selected container.' >&2; exit 1; }

printf 'Preflight passed: context=%s namespace=%s deployment=%s container=%s current=%s target=%s keySecret=%s pvc=%s\n' \
  "$KUBE_CONTEXT" "$KUBE_NAMESPACE" "$KUBE_DEPLOYMENT" "$KUBE_CONTAINER" \
  "$current_image" "$SCRATCHPAD_IMAGE" "$key_secret" "$pvc"
[ "$mode" = --check ] && exit 0
[ -n "${SCRATCHPAD_BACKUP_REF:-}" ] && [ "${SCRATCHPAD_BACKUP_VERIFIED:-}" = yes ] || { echo 'A verified pre-change database backup reference is required.' >&2; exit 2; }
[ "${SCRATCHPAD_MIGRATION_STATE:-}" = complete ] || { echo 'Complete the selected-image migration procedure before rollout.' >&2; exit 2; }
if [ "$mode" = --switch-to-go ]; then
  [ "${SCRATCHPAD_RUNTIME_SWITCH_APPROVED:-}" = yes ] || { echo 'Set SCRATCHPAD_RUNTIME_SWITCH_APPROVED=yes after separate owner approval.' >&2; exit 2; }
  container_index=$(printf '%s' "$deployment" | jq -er --arg name "$KUBE_CONTAINER" '[.spec.template.spec.containers[].name] | index($name)')
  patch=$(printf '%s' "$deployment" | jq -c --arg image "$SCRATCHPAD_IMAGE" --argjson index "$container_index" '
    [{op:"replace",path:("/spec/template/spec/containers/" + ($index|tostring) + "/image"),value:$image}]
    + (if (.spec.template.spec.containers[$index] | has("command")) then [{op:"remove",path:("/spec/template/spec/containers/" + ($index|tostring) + "/command")}] else [] end)
    + (if (.spec.template.spec.containers[$index] | has("args")) then [{op:"remove",path:("/spec/template/spec/containers/" + ($index|tostring) + "/args")}] else [] end)')
  kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" patch \
    "deployment/$KUBE_DEPLOYMENT" --type=json -p "$patch"
else
  [ "${SCRATCHPAD_CHANGE_APPROVED:-}" = yes ] || { echo 'Set SCRATCHPAD_CHANGE_APPROVED=yes after owner approval.' >&2; exit 2; }
kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" set image \
  "deployment/$KUBE_DEPLOYMENT" "$KUBE_CONTAINER=$SCRATCHPAD_IMAGE"
fi
kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" rollout status \
  "deployment/$KUBE_DEPLOYMENT" --timeout=10m
