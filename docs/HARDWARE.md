# Supported hardware

Ember draws on an [AWTRIX NG](https://github.com/Blueforcer/awtrix-ng) clock
over the firmware's HTTP API. It does not support AWTRIX 3.

| Hardware | Status |
|---|---|
| Ulanzi TC001 on AWTRIX NG | Supported. The reference device. |
| Ulanzi TC002 on AWTRIX NG | Planned, see [#839](https://github.com/tarakanof/ember/issues/839). |
| Self-built AWTRIX NG clock (8 rows) | 32 columns expected to work; wider panels are untested. |
| AWTRIX 3 firmware (any device) | Not supported. AWTRIX 3 is archived upstream. |
| cinder knob | Optional companion, see below. |

## Flashing a TC001

Flash AWTRIX NG by following the [awtrix-ng docs](https://blueforcer.github.io/awtrix-ng/).
To move a TC001 over from AWTRIX 3, back up first: [`RUNBOOK.md`](RUNBOOK.md) →
"awtrix-ng flashing, backup, and first-boot". Ember finds the clock by mDNS; choose it in
the app under Settings › Devices › Clock › Discover Clocks…. The clock and the
Mac must be on the same network.

## The cinder knob

[cinder](https://github.com/tarakanof/cinder) is firmware for a round knob with
a small screen that shows the same status and controls the Pomodoro. It is a
build-it-yourself companion in its own repository; no kit is sold. The contract
between the two is [`DEVICE-PROTOCOL.md`](DEVICE-PROTOCOL.md).

## Firmware licence

AWTRIX NG is source-available under PolyForm Noncommercial. Ember is MIT; it
talks to the firmware over HTTP and ships none of it. Ember is not affiliated
with or endorsed by Ulanzi or the AWTRIX firmware authors.
