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
