#!/usr/bin/env bash
set -euo pipefail
image=${1:-scratchpad:ci}
name="scratchpad-smoke-${RANDOM}"
volume="${name}-data"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT
# The image has no HTTP client, so requests go through bash's /dev/tcp.
request() {
  docker exec "$name" bash -c "exec 3<>/dev/tcp/127.0.0.1/3000 && printf 'GET $1 HTTP/1.0\r\n\r\n' >&3 && cat <&3"
}
ready() {
  for attempt in $(seq 1 40); do
    if request /ready 2>/dev/null | head -n 1 | grep -q ' 200 '; then return; fi
    if [ "$attempt" -eq 40 ]; then docker logs "$name"; exit 1; fi
    sleep 1
  done
}
docker volume create "$volume" >/dev/null
docker run -d --name "$name" -v "$volume:/data" "$image" >/dev/null
ready
request /api/v1/records | head -n 1 | grep -q ' 401 '
request / | grep -q '<title>Scratchpad'
if docker exec "$name" sh -c 'command -v node' >/dev/null 2>&1; then
  printf 'The image still contains a Node runtime.\n' >&2
  exit 1
fi
processes=$(docker top "$name" | tail -n +2)
if [ "$(printf '%s\n' "$processes" | wc -l)" -ne 1 ] || ! printf '%s\n' "$processes" | grep -q 'scratchpad-server serve'; then
  printf '%s\nThe container runs more than the Go server.\n' "$processes" >&2
  exit 1
fi
docker restart "$name" >/dev/null
ready
printf 'Container readiness, authenticated boundary, embedded dashboard, single process and restart passed.\n'
