# Ember (native SwiftUI menu-bar app)

The native macOS menu-bar companion for `ember`, replacing the retired Go +
`fyne.io/systray` + DarwinKit menu app. The Go server and HTTP
API are unchanged; this app is a pure client.

## Structure (hybrid: SwiftPM core + thin Xcode app)

- **`Package.swift` + `Sources/EmberKit/`** — all testable logic, no SwiftUI
  scene code: Codable models (mirror the server wire shapes), `APIClient`,
  Pomodoro/Status/Preview services, `pickWinning`, `EnvFile` + validation
  (`producer.env`), `ConnectionSettings`/`DisplaySettings`, `MenuPrefs`, and the
  `@Observable AppModel` + poller. Tested headlessly with `swift test`.
- **`Ember/`** — the thin SwiftUI app target (`LSUIElement` agent app):
  `MenuBarExtra` (status + Pomodoro controls + dynamic tray glyph), a `Settings`
  sidebar window (Connection / Device / Code agent / Pomodoro / Weather /
  Reminders / App), and a status + preview **dashboard** `Window`. `AppEnvironment`
  owns the live `APIClient`, models, services, prefs, and `ServerDiscovery`
  (Bonjour `_ember._tcp` browse). The **Device** tab drives the AWTRIX clock's own
  settings via the server's `/v1/device/*` proxy (incl. an mDNS clock picker);
  Connection lists Bonjour-discovered servers (with a Local Network grant prompt).
- **`project.yml`** — XcodeGen source for the app target (committed). The generated
  `Ember.xcodeproj` is **gitignored** — regenerate it (below).

## Build & run

```sh
brew install xcodegen                                  # one-time
xcodegen generate --spec macos/project.yml --project macos
open macos/Ember.xcodeproj                        # select your Dev team, then Run (⌘R)
```

Headless checks (CI-friendly, no signing):

```sh
swift test --package-path macos                        # the EmberKit unit tests
xcodebuild -project macos/Ember.xcodeproj -scheme Ember \
  -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build
```

## Configuration

Reads `~/.config/ember/producer.env` (`EMBER_SERVER_URL`,
`EMBER_TOKEN`, `EMBER_SOURCE`, `EMBER_SOURCE_COLOR`, and the display toggles),
shared with the Go producers. The Connection tab edits it and rebuilds the live
client without relaunch. Menu-only prefs (icon palette, tray glyphs) live in
`UserDefaults`.

The live Display/dashboard previews call the server's open `GET /v1/preview`
endpoint — your server must be on a build that includes it (added 2026-05; a
pre-`/v1/preview` server returns 401 for that route).

## Status (2026-06-10)

All settings panes + dashboard are built; `swift test` is green; the installed
SwiftUI app is the live menu (the Go menu is long retired). Latest additions:
the Device tab (clock control + mDNS clock picker), Connection server discovery
with a Local Network grant prompt, and an App→About version readout. App
`MARKETING_VERSION` 0.4.0.
