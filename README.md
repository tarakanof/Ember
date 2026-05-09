# AWTRIX AI Status

Small status aggregator for showing Claude/Codex activity on an AWTRIX3 clock.

The service is intentionally light:

- one Go binary, stdlib only
- single AWTRIX HTTP transport
- bearer-token auth on write endpoints
- HTTP input endpoint for laptop-side producers

## Current Target

The first target clock discovered in Home Assistant:

- AWTRIX prefix: `awtrix_05ffb8`
- AWTRIX HTTP URL: `http://192.168.0.14`
- Firmware: `0.98`

## Local Go

Built with the Homebrew Go toolchain:

```sh
go version
# go version go1.26.3 darwin/arm64
```

## Run Locally

```sh
cp config.example.json config.json
STATUS_TOKEN=dev-token go run ./cmd/awtrix-ai-status -config config.json
```

Post a demo running status:

```sh
curl -X POST http://localhost:8080/v1/status \
  -H 'Authorization: Bearer dev-token' \
  -H 'Content-Type: application/json' \
  -d '{"source":"dt-mbp","tool":"codex","session":"awtrix","state":"running","message":"building"}'
```

Post a waiting approval:

```sh
curl -X POST http://localhost:8080/v1/status \
  -H 'Authorization: Bearer dev-token' \
  -H 'Content-Type: application/json' \
  -d '{"source":"dt-mbp","tool":"claude","session":"desktop","state":"waiting","message":"approve Bash"}'
```

Drop a single session:

```sh
curl -X DELETE http://localhost:8080/v1/status \
  -H 'Authorization: Bearer dev-token' \
  -H 'Content-Type: application/json' \
  -d '{"source":"dt-mbp","tool":"claude","session":"desktop"}'
```

Wipe everything (admin):

```sh
curl -X POST http://localhost:8080/v1/clear -H 'Authorization: Bearer dev-token'
```

Inspect current state (no auth):

```sh
curl http://localhost:8080/state | jq
```

## Protocol

The wire-protocol contract is in `docs/superpowers/specs/2026-05-08-protocol-contract-design.md`. Summary:

- Required fields: `source` (laptop ID), `tool` (`claude` | `codex` | …), `session`, `state` (`idle` | `running` | `waiting` | `done` | `error`).
- Producer emit policy: event on every transition, plus a 10s heartbeat while `running`/`waiting`.
- Server reaps idle sessions after `stale_seconds` (default 25s); `done`/`error` linger for `done_ttl_seconds` (default 30s).
- Write endpoints require `Authorization: Bearer <STATUS_TOKEN>`. Empty `STATUS_TOKEN` disables auth.
- Read endpoints (`GET /state`, `GET /healthz`) are always unauthenticated.

## Container image

The image is multi-stage and multi-arch (linux/amd64 + linux/arm64):
alpine builder → distroless `static-debian12:nonroot` runtime, ~7 MB,
runs as uid 65532, no shell.

**Build (single-arch dev):**
```sh
docker buildx build --platform linux/amd64 -t awtrix-ai-status:dev --load .
```

**Build (multi-arch, intended target):**
```sh
docker buildx build --platform linux/amd64,linux/arm64 \
  -t awtrix-ai-status:0.1.0 .
```
(no `--load` because Docker can't multi-load locally; use `--push` once
a registry is wired up — Phase 2.)

**Run:**
```sh
docker run --rm -d --name awtrix-ai-status \
  -p 8080:8080 \
  -e STATUS_TOKEN="$(cat ~/.config/awtrix-ai-status/token)" \
  -v /path/to/config.json:/etc/awtrix-ai-status/config.json:ro \
  awtrix-ai-status:dev
```

**Operator commands:**
```sh
docker exec awtrix-ai-status /awtrix-ai-status version
docker exec awtrix-ai-status /awtrix-ai-status doctor                 # full diagnostic
docker exec awtrix-ai-status /awtrix-ai-status --print-config         # what's in effect, secrets redacted
docker exec awtrix-ai-status /awtrix-ai-status healthcheck && echo OK
docker logs awtrix-ai-status

# Operator HTTP (read):
curl http://localhost:8080/version
curl -H "Authorization: Bearer $STATUS_TOKEN" http://localhost:8080/admin/doctor

# Operator HTTP (mutate): hot-reload config without restart.
# Edit your bind-mounted config.json, then:
curl -X POST -H "Authorization: Bearer $STATUS_TOKEN" \
  http://localhost:8080/admin/reload
```

The binary's `healthcheck` subcommand defaults to probing
`http://127.0.0.1:8080/healthz`. If you bind the server to a non-default
port via `config.json`, set `STATUS_HEALTHCHECK_URL` to match.

The `doctor` subcommand defaults to `http://127.0.0.1:8080/admin/doctor`.
Override with `--server-url`. Use `--offline` for pre-flight checks before
starting the server.

**TLS (optional):** set both `STATUS_TLS_CERT_FILE` and `STATUS_TLS_KEY_FILE`
in the container environment to enable HTTPS. Setting only one is a
startup error; setting neither keeps the server on plain HTTP. The cert is
loaded once at startup — rotation requires a container restart. For a
self-signed homelab cert, issue it with `subjectAltName=IP:127.0.0.1` so
the in-image healthcheck can validate the loopback target. Two helper
env vars exist for the in-image healthcheck client:

- `STATUS_HEALTHCHECK_CA_FILE` — path to a PEM bundle to add to the trust
  pool (e.g. your homelab CA).
- `STATUS_HEALTHCHECK_INSECURE=1` — skip TLS verification entirely. Fine
  on a trusted LAN; the recommended easy path for self-signed setups.

The runtime image runs as UID 65532 (distroless `nonroot`). A cert mounted
read-only as `0600 root:root` will fail to read. Either `chmod 0644` the
files, `chown 65532` them, or drop them into a volume that the runtime
can read. Example:

```sh
docker run --rm -d --name awtrix-ai-status \
  -p 8443:8080 \
  -e STATUS_TOKEN="$(cat ~/.config/awtrix-ai-status/token)" \
  -e STATUS_TLS_CERT_FILE=/certs/cert.pem \
  -e STATUS_TLS_KEY_FILE=/certs/key.pem \
  -e STATUS_HEALTHCHECK_INSECURE=1 \
  -v /path/to/config.json:/etc/awtrix-ai-status/config.json:ro \
  -v /path/to/certs:/certs:ro \
  awtrix-ai-status:dev
```

`/admin/doctor`'s `http_listening` detail reflects the live scheme
(`scheme=https` when TLS is on, `scheme=http` otherwise).

**Prometheus `/metrics`:** the server always exposes `GET /metrics` on
the public mux (no auth, never rate-limited). The body is Prometheus
text exposition format:

- counters: `awtrix_requests_total{pattern,status}`,
  `awtrix_publish_total{result}`, `awtrix_rate_limit_denied_total`,
  `awtrix_sessions_evicted_total`
- gauges: `awtrix_sessions_active`, `awtrix_uptime_seconds`,
  `awtrix_last_publish_unix`, `awtrix_last_publish_ok`,
  `awtrix_ratelimit_buckets`, `awtrix_build_info{revision,go_version}`

Cardinality is bounded — request counts are labelled by Go 1.22's
matched route pattern, not by URL path, so a 404 spammer can't blow up
series. Requests rejected by `requireAuth` / `adminRequireAuth` count
against the outer prefix (`/v1/`, `/admin/`) rather than the specific
route. The endpoint deliberately doesn't export Go runtime metrics
(goroutines, GC) — that would require `prometheus/client_golang`,
which we skip in line with the stdlib-only choice.

Scrape config snippet (Prometheus):

```yaml
- job_name: awtrix-ai-status
  scrape_interval: 15s
  static_configs:
    - targets: ['homelab.lan:8080']
```

**Rate limiting:** the server enforces a per-source-IP token-bucket
rate limit on `/v1/*` writes. Defaults: 10-token burst, 2 tokens/sec
sustained refill per IP, 5-minute idle-bucket eviction. `/admin/*`
and read endpoints (`/healthz`, `/state`, `/version`) are not
rate-limited. Tune via the `rate_limit` section of `config.json`
and reload with `POST /admin/reload`. To disable entirely, set
`rate_limit.disabled: true`. Note that `scripts/image-smoke.sh`
does not exercise `/v1/*` writes, so it does not validate the
rate-limit code path — that is covered by unit tests instead.

**Smoke test (requires Docker):**
```sh
./scripts/image-smoke.sh
```
The script skips with a friendly message when Docker is missing.

## Config

```json
{
  "http": { "addr": ":8080" },
  "awtrix": {
    "http_base_url": "http://192.168.0.14",
    "app_name": "ai_status",
    "timeout_seconds": 5
  },
  "auth": { "status_token_env": "STATUS_TOKEN" },
  "rate_limit": {
    "disabled": false,
    "burst": 10,
    "refill_per_sec": 2,
    "idle_evict_seconds": 300
  },
  "display": {
    "idle_text": "AI idle",
    "stale_seconds": 25,
    "done_ttl_seconds": 30,
    "heartbeat_seconds": 10,
    "refresh_seconds": 5,
    "notify_on_waiting": false
  }
}
```
