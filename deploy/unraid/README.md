# Ember — Unraid install

Some Unraid builds don't accept a pasted **Template URL**. The reliable way is to
drop the template file into Unraid's user-templates folder, then pick it from the
**Add Container → Template** dropdown.

## Install the template (Unraid host shell — Terminal plugin or SSH)

    curl -fsSL -o /boot/config/plugins/dockerMan/templates-user/my-ember.xml \
      https://raw.githubusercontent.com/tarakanof/Ember/main/deploy/unraid/ember.xml

Then in the web UI: **Docker → Add Container**, and in the **Template** dropdown
choose **ember** (listed under *User templates*). The form is prefilled; fill in
the fields below and Apply.

Ember publishes to Docker Hub (`dtarakanov/ember`), so there is **no GHCR
visibility step**.

## First install (do this before clicking Apply)
1. Host shell: `mkdir -p /mnt/user/appdata/ember && curl -fsSL -o /mnt/user/appdata/ember/config.json https://raw.githubusercontent.com/tarakanof/Ember/main/config.example.json`
2. Edit `config.json` → set `awtrix.http_base_url` to your AWTRIX device URL.
3. `openssl rand -hex 32` → paste into the **EMBER_TOKEN** field.
4. Apply.

## Verify (host shell — runtime image is distroless, no Console shell)
    docker exec ember /ember doctor
All checks `[OK]`.
