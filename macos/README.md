# AwtrixMenu (native SwiftUI menu-bar app)

The native macOS menu-bar companion for `awtrix-ai-status`, replacing the Go +
`fyne.io/systray` + DarwinKit app under `cmd/awtrix-menu`. The Go server and HTTP
API are unchanged; this app is a pure client.

## Structure (hybrid: SwiftPM core + thin Xcode app)

- **`Package.swift` + `Sources/AwtrixMenuKit/`** — all testable logic, no SwiftUI
  scene code: Codable models (mirror the server wire shapes), `APIClient`,
  Pomodoro/Status/Preview services, `pickWinning`, `EnvFile` + validation
  (`producer.env`), `ConnectionSettings`/`DisplaySettings`, `MenuPrefs`, and the
  `@Observable AppModel` + poller. Tested headlessly with `swift test`.
- **`AwtrixMenu/`** — the thin SwiftUI app target (`LSUIElement` agent app):
  `MenuBarExtra` (status + Pomodoro controls + dynamic tray glyph), a `Settings`
  scene with four tabs (Connection / Display / Pomodoro / App), and a status +
  preview **dashboard** `Window`. `AppEnvironment` owns the live `APIClient`,
  models, services, and prefs.
- **`project.yml`** — XcodeGen source for the app target (committed). The generated
  `AwtrixMenu.xcodeproj` is **gitignored** — regenerate it (below).

## Build & run

```sh
brew install xcodegen                                  # one-time
xcodegen generate --spec macos/project.yml --project macos
open macos/AwtrixMenu.xcodeproj                        # select your Dev team, then Run (⌘R)
```

Headless checks (CI-friendly, no signing):

```sh
swift test --package-path macos                        # the AwtrixMenuKit unit tests
xcodebuild -project macos/AwtrixMenu.xcodeproj -scheme AwtrixMenu \
  -destination 'platform=macOS' CODE_SIGNING_ALLOWED=NO build
```

## Configuration

Reads `~/.config/awtrix-ai-status/producer.env` (`STATUS_SERVER_URL`,
`STATUS_TOKEN`, `STATUS_SOURCE`, `STATUS_SOURCE_COLOR`, and the display toggles),
shared with the Go producers. The Connection tab edits it and rebuilds the live
client without relaunch. Menu-only prefs (icon palette, tray glyphs) live in
`UserDefaults`.

The live Display/dashboard previews call the server's open `GET /v1/preview`
endpoint — your server must be on a build that includes it (added 2026-05; a
pre-`/v1/preview` server returns 401 for that route).

## Status (2026-05-30)

Settings (all four tabs) + dashboard are built; `swift test` is green; the app
launches and the tray glyph shows per-state colour. **Not yet cut over** — the Go
`cmd/awtrix-menu` is still the installed menu. Cutover (install this app, retire
`com.awtrix-ai-status.menu`, delete the Go menu + DarwinKit/systray deps) is the
remaining Phase D. See the phase specs/plans in the Obsidian vault
(`Superpowers Specs/awtrix-ai-status/2026-05-*-menu-swiftui-port-*`).
