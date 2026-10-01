#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not installed; skipping image smoke test." >&2
  exit 0
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

IMG="ember:smoke"
NAME="ember-smoke-$$"

docker buildx build --load -t "$IMG" .
trap '
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker rmi -f "$IMG" >/dev/null 2>&1 || true
' EXIT

docker run -d --rm --name "$NAME" \
  -p 13627:3627 \
  -e EMBER_TOKEN=smoke-token \
  -v "$REPO_ROOT/config.example.json":/etc/ember/config.json:ro \
  "$IMG"

ready=0
deadline=$(( $(date +%s) + 10 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if docker exec "$NAME" /ember healthcheck >/dev/null 2>&1; then
    ready=1
    echo "smoke: in-image healthcheck OK"
    break
  fi
  sleep 1
done
if [ "$ready" -ne 1 ]; then
  echo "smoke: FAIL — in-image healthcheck never succeeded within 10s" >&2
  docker logs "$NAME" >&2 || true
  exit 1
fi

curl -fsS http://localhost:13627/healthz >/dev/null
echo "smoke: /healthz from host OK"

ver="$(docker exec "$NAME" /ember version)"
echo "smoke: version output: $ver"
if echo "$ver" | grep -q "unknown"; then
  echo "smoke: FAIL — version subcommand reports 'unknown' (likely .git missing from build context)" >&2
  exit 1
fi

# - /version HTTP endpoint should return JSON with our binary name.
ver_http="$(curl -fsS http://localhost:13627/version)"
echo "smoke: /version output: $ver_http"
if ! echo "$ver_http" | grep -q '"binary":"ember"'; then
  echo "smoke: FAIL — /version missing binary field" >&2
  exit 1
fi

docker exec -e CONFIG_PATH=/etc/ember/config.json "$NAME" \
  /ember --print-config -config /etc/ember/config.json > /tmp/printcfg.json
if ! jq -e '.awtrix.http_base_url' /tmp/printcfg.json >/dev/null; then
  echo "smoke: FAIL — --print-config output missing awtrix.http_base_url" >&2
  cat /tmp/printcfg.json >&2
  exit 1
fi
rm -f /tmp/printcfg.json
echo "smoke: --print-config OK"

metrics_body="$(curl -fsS http://localhost:13627/metrics)"
if ! echo "$metrics_body" | grep -q "ember_build_info"; then
  echo "smoke: FAIL — /metrics body missing ember_build_info" >&2
  echo "$metrics_body" >&2
  exit 1
fi
echo "smoke: /metrics OK"

status_resp_code="$(curl -fsS -o /tmp/smoke_status.json -w '%{http_code}' \
  -X POST http://localhost:13627/v1/status \
  -H 'Authorization: Bearer smoke-token' \
  -H 'Content-Type: application/json' \
  -d '{"source":"smoke","tool":"claude","session":"s1","state":"idle","context_pct":42,"source_color":"#aa66ff"}')"
if [ "$status_resp_code" != "200" ]; then
  echo "smoke: FAIL — POST /v1/status returned $status_resp_code, want 200" >&2
  cat /tmp/smoke_status.json >&2 || true
  exit 1
fi
rm -f /tmp/smoke_status.json
echo "smoke: POST /v1/status with context_pct + source_color OK"

echo "smoke: PASS"
