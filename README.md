# AWTRIX AI Status

Small status aggregator for showing Claude/Codex activity on an AWTRIX3 clock.

The service is intentionally light:

- one Go binary
- no runtime dependencies
- no Go package dependencies beyond the standard library
- configurable AWTRIX output over HTTP or MQTT
- HTTP input endpoint for laptop-side agents

## Current Target

The first target clock discovered in Home Assistant:

- AWTRIX prefix: `awtrix_05ffb8`
- AWTRIX HTTP URL: `http://192.168.0.14`
- Firmware: `0.98`

## Local Go

This project is currently built with the Homebrew Go toolchain:

```sh
go version
# go version go1.26.3 darwin/arm64
```

## Run Locally

```sh
cp config.example.json config.json
go run ./cmd/awtrix-ai-status -config config.json
```

Post a demo status:

```sh
curl -X POST http://localhost:8080/v1/status \
  -H 'Content-Type: application/json' \
  -d '{"source":"dt-macbook","tool":"codex","session":"repo-awtrix","state":"running","message":"building"}'
```

Post a waiting approval:

```sh
curl -X POST http://localhost:8080/v1/status \
  -H 'Content-Type: application/json' \
  -d '{"source":"dt-macbook","tool":"claude","session":"desktop","state":"waiting","message":"approve Bash"}'
```

Clear session state:

```sh
curl -X POST http://localhost:8080/v1/clear
```

## Docker

```sh
docker build -t awtrix-ai-status .
docker run --rm -p 8080:8080 \
  -v "$PWD/config.json:/config/config.json:ro" \
  awtrix-ai-status
```

For Unraid, use a bind mount for `/config/config.json` and expose port `8080`.

## Config

HTTP output is simplest and works directly with the clock:

```json
{
  "awtrix": {
    "transport": "http",
    "http_base_url": "http://192.168.0.14"
  }
}
```

MQTT output avoids depending on the clock IP:

```json
{
  "awtrix": {
    "transport": "mqtt",
    "mqtt_prefix": "awtrix_05ffb8"
  },
  "mqtt": {
    "addr": "192.168.0.36:1883",
    "username_env": "MQTT_USERNAME",
    "password_env": "MQTT_PASSWORD"
  }
}
```

The built-in MQTT publisher only publishes QoS 0 messages, which is enough for AWTRIX custom apps and indicators.
