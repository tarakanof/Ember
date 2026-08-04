# Berry experiments

Scripts in this directory are **not features**. Nothing provisions them, nothing
embeds them, and no Go code references them. They exist as the recorded artefact
of an experiment; they are installed by hand, observed, and deleted again.

Anything that graduates to a feature moves up one directory, next to
`ember-boot-ping.be`, and gets an embed + a provisioning path in `cmd/ember`.

Install / observe / remove:

```bash
curl -sX PUT "http://$AWTRIX/api/v1/apps/script/zzz-ember-weather" \
  -H 'Content-Type: text/plain' --data-binary @zzz-ember-weather.be
curl -sX PUT "http://$AWTRIX/api/v1/apps/active" \
  -H 'Content-Type: application/json' -d '{"name":"zzz-ember-weather","fast":true}'
curl -s "http://$AWTRIX/api/v1/display/screen"
curl -s "http://$AWTRIX/api/v1/logs"
curl -sX DELETE "http://$AWTRIX/api/v1/apps/zzz-ember-weather"
```

The `zzz-` prefix is the convention for "temporary, delete me": it sorts last in
the device's app list and is unmistakable next to a real app name.

## zzz-ember-weather.be

Asks whether Ember's weather tile could be rendered **on the device** instead of
rendered server-side and pushed. Same layout as `internal/render`'s tile: a
condition glyph in the left 8 columns, the temperature centred in the rest, a
24-hour trend strip along the bottom two rows.

It polls **Open-Meteo directly**, not Ember, and that is the experiment's first
finding: Ember has no public endpoint that serves weather as *data*.
`GET /v1/weather/preview` — the obvious candidate — serves a rendered pixel
frame: ~7.8 KB of `"#rrggbb"` strings, three frames, no scalars. It is at the
device's 8 KB HTTP body cap before `find`/`keep` can help, and there is no needle
that yields a temperature. Moving this tile on-device for real would mean adding
a scalar weather endpoint to the server first.

Measurements from awtrix-ng 1.0.13 on a Ulanzi TC001 (no PSRAM, 96 KB Berry
heap) are in the issue #73 report. Headlines: 4.5 KB of source cost **~5.8 KB of
Berry heap** and a transient ~12 KB of ESP32 heap to compile; a failed fetch is
completely silent and the tile silently keeps its last stored value.
