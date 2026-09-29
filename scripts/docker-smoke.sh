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
docker volume create "$volume" >/dev/null
docker run -d --name "$name" -v "$volume:/data" "$image" >/dev/null
for attempt in $(seq 1 40); do
  if docker exec "$name" node -e "fetch('http://localhost:3000/ready',{signal:AbortSignal.timeout(5000)}).then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"; then break; fi
  if [ "$attempt" -eq 40 ]; then docker logs "$name"; exit 1; fi
  sleep 1
done
docker exec "$name" node -e "fetch('http://localhost:3000/api/v1/records',{signal:AbortSignal.timeout(5000)}).then(r=>process.exit(r.status===401?0:1))"
docker exec "$name" node -e "fetch('http://localhost:3000/',{signal:AbortSignal.timeout(5000)}).then(async r=>{if(!r.ok||!(await r.text()).includes('Scratchpad'))process.exit(1)})"
docker restart "$name" >/dev/null
for attempt in $(seq 1 40); do
  if docker exec "$name" node -e "fetch('http://localhost:3000/ready',{signal:AbortSignal.timeout(5000)}).then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"; then break; fi
  if [ "$attempt" -eq 40 ]; then docker logs "$name"; exit 1; fi
  sleep 1
done
printf 'Container readiness, authenticated boundary, page rendering and restart passed.\n'
