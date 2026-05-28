# TODO / pick-up-later

Open items left after the 2026-05-26/28 session (white-robot fix + settings-window work).

## Heartbeat agent reliability (highest priority)
The `com.awtrix-ai-status.heartbeat` LaunchAgent **silently unloaded twice** during the
session (was `runs=203`, then gone — `launchctl print` returned "Could not find service").
Root cause unknown (suspect sleep/wake or a launchd session event). It is not in the
disabled list and the plist has `RunAtLoad=true` + `StartInterval=10`.

Current mitigation: the coordinator stale window is 300s, so a heartbeat lapse only idles
the display after ~5 min of true inactivity (not 25s). But a *prolonged* outage still idles.

Next steps (pick one or more):
- Investigate the unload trigger (check `log show --predicate 'process == "launchd"'`
  around an unload; correlate with sleep/wake via `pmset -g log`).
- Self-heal: have `awtrix-claude-producer tick` (or `doctor --fix`) re-bootstrap the agent
  if it finds itself unloaded. (tick already runs every 10s *when loaded* — so self-heal
  needs an external nudge; e.g. the menu app on launch, or a separate KeepAlive watchdog.)
- Consider whether `install` should set the agent up more robustly.

## Container image hygiene
- Live container runs image `awtrix-ai-status:stale300` (ad-hoc tag from the stale-window
  rebuild). Older tags `awtrix-ai-status:glass-ltr` and `:reset-card` still exist.
- Retag the current image to something stable (e.g. `:local` or a version) and
  `docker image prune` the stale ones. The container was created via ad-hoc `docker run`
  (no compose/deploy script) — config captured in `docker inspect awtrix-ai-status`.

## Settings window (nice-to-have)
- "Tool / approval detail" and "Recent-activity trail" rows have **no thumbnail** — their
  content is scrolling text with no static placement. Could add a marker across the
  number-slot area to indicate "this region scrolls text."

## Notes
- Verification method that works well here: a background shell loop polling `/state`
  (its `curl`s don't fire Claude hooks, so it's a clean hook-free observer of session age).
  See the monitor approach used in the session — age sawtoothing ~5–10s = heartbeat healthy;
  monotonic climb to ~25s then ABSENT = nothing refreshing it.
