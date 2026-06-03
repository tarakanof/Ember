# Ember — Unraid install

Add via **Docker → Add Container → Template URL**:

    https://raw.githubusercontent.com/tarakanof/ember/main/deploy/unraid/ember.xml

Ember publishes to Docker Hub (`dtarakanov/ember`) — public on first release, so
there is **no GHCR visibility step**.

## First install
1. Host shell: `mkdir -p /mnt/user/appdata/ember && curl -fsSL -o /mnt/user/appdata/ember/config.json https://raw.githubusercontent.com/tarakanof/ember/main/config.example.json`
2. Edit `config.json` → set `awtrix.http_base_url` to your AWTRIX device URL.
3. `openssl rand -hex 32` → paste into **EMBER_TOKEN**.
4. Apply.

## Verify (host shell — runtime image is distroless, no Console shell)
    docker exec ember /ember doctor
All checks `[OK]`.
