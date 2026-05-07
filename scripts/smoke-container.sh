#!/usr/bin/env bash
set -euo pipefail

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found; skipping container smoke test"
  exit 0
fi

IMAGE="${IMAGE:-freesync:smoke}"
PORT="${PORT:-18080}"
TOKEN="${TOKEN:-smoketoken}"

mkdir -p data

docker build -t "${IMAGE}" .
CID=$(docker run -d \
  -p "${PORT}:8080" \
  -v "$(pwd)/config:/app/config:ro" \
  -v "$(pwd)/data:/app/data" \
  "${IMAGE}" serve -token "${TOKEN}")

cleanup() {
  docker rm -f "${CID}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for i in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null; then
    break
  fi
  sleep 1
done

curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null

code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:${PORT}/run")
if [[ "${code}" != "401" ]]; then
  echo "expected unauthorized status 401, got ${code}"
  exit 1
fi

curl -fsS -X POST "http://127.0.0.1:${PORT}/run?apply=false" \
  -H "Authorization: Bearer ${TOKEN}" >/dev/null

echo "container smoke test passed"
