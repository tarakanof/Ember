#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not installed; skipping image smoke test." >&2
  exit 0
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

IMG="awtrix-ai-status:smoke"
NAME="awtrix-ai-status-smoke-$$"

# Use the same buildx engine as the documented production build path.
docker buildx build --load -t "$IMG" .
trap '
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker rmi -f "$IMG" >/dev/null 2>&1 || true
' EXIT

docker run -d --rm --name "$NAME" \
  -p 18080:8080 \
  -e STATUS_TOKEN=smoke-token \
  -v "$REPO_ROOT/config.example.json":/etc/awtrix-ai-status/config.json:ro \
  "$IMG"

# Wait up to 10s for the in-image healthcheck. Track success explicitly so
# a never-ready container fails the script — falling through to the host
# probe could mask a broken `healthcheck` subcommand.
ready=0
deadline=$(( $(date +%s) + 10 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if docker exec "$NAME" /awtrix-ai-status healthcheck >/dev/null 2>&1; then
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

# Probe /healthz from the host as well (verifies port mapping + handler).
curl -fsS http://localhost:18080/healthz >/dev/null
echo "smoke: /healthz from host OK"

# Verify the version subcommand surfaces a real VCS revision (not "unknown"),
# proving .git made it into the builder context.
ver="$(docker exec "$NAME" /awtrix-ai-status version)"
echo "smoke: version output: $ver"
if echo "$ver" | grep -q "unknown"; then
  echo "smoke: FAIL — version subcommand reports 'unknown' (likely .git missing from build context)" >&2
  exit 1
fi

# Extra E.2a probes:
# - /version HTTP endpoint should return JSON with our binary name.
ver_http="$(curl -fsS http://localhost:18080/version)"
echo "smoke: /version output: $ver_http"
if ! echo "$ver_http" | grep -q '"binary":"awtrix-ai-status"'; then
  echo "smoke: FAIL — /version missing binary field" >&2
  exit 1
fi

# - --print-config should produce parseable JSON with redacted secrets.
docker exec -e CONFIG_PATH=/etc/awtrix-ai-status/config.json "$NAME" \
  /awtrix-ai-status --print-config -config /etc/awtrix-ai-status/config.json > /tmp/printcfg.json
if ! jq -e '.awtrix.http_base_url' /tmp/printcfg.json >/dev/null; then
  echo "smoke: FAIL — --print-config output missing awtrix.http_base_url" >&2
  cat /tmp/printcfg.json >&2
  exit 1
fi
rm -f /tmp/printcfg.json
echo "smoke: --print-config OK"

# - /metrics endpoint should return a Prometheus exposition body that
#   includes awtrix_build_info (always present, regardless of activity).
metrics_body="$(curl -fsS http://localhost:18080/metrics)"
if ! echo "$metrics_body" | grep -q "awtrix_build_info"; then
  echo "smoke: FAIL — /metrics body missing awtrix_build_info" >&2
  echo "$metrics_body" >&2
  exit 1
fi
echo "smoke: /metrics OK"

echo "smoke: PASS"
