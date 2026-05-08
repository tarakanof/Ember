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

## Docker

```sh
docker build -t awtrix-ai-status .
docker run --rm -p 8080:8080 \
  -e STATUS_TOKEN=your-token \
  -v "$PWD/config.json:/config/config.json:ro" \
  awtrix-ai-status
```

For Unraid, use a bind mount for `/config/config.json`, expose port `8080`, and set `STATUS_TOKEN` as an env var.

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
