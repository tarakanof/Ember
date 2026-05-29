# Awtrix Manager — macOS Menu Bar Icons

Monochrome **template images** for `NSStatusItem`. macOS tints them automatically
(dark in light menus, white in dark menus, white on tinted wallpapers).

```
menubar/
├─ svg/   <name>-template.svg        vector (other platforms / scaling)
└─ png/   <name>-template.png        @1x · 18×18
          <name>-template-2x.png     @2x · 36×36   (rename to @2x — see below)
```

## Icons

| name           | what it is                                  |
|----------------|---------------------------------------------|
| `awtrix`       | LED pixel-panel mark (default A)            |
| `awtrix-screen`| framed screen + pixels (default A2)         |
| `aicode`       | terminal + spark — for Claude Code          |
| `aicode-chat`  | spark in chat bubble (alt)                  |
| `code`         | angle brackets `</>` — for Codex            |
| `code-hex`     | hexagon + brackets (alt)                    |
| `pomodoro`     | tomato timer                                |

> The `aicode` / `code` glyphs are **original** marks, not the Anthropic / OpenAI
> logos (those are trademarked). Swap in official brand SVGs if you have a license.

## Use in AppKit

```swift
let img = NSImage(named: "awtrix-template")!   // or load the PNG
img.isTemplate = true                          // ← enables auto tint
statusItem.button?.image = img
```

The PNGs are already pure black + alpha, so `isTemplate = true` is all you need.
Add both the `@1x` and `@2x` files to your asset catalog / bundle.

### Fix the @2x names

The `-2x` suffix must be `@2x` for macOS to pick the retina variant:

```bash
cd menubar/png
for f in *-2x.png; do mv "$f" "${f/-2x/@2x}"; done
```

## Windows / cross-platform

The SVGs work directly in WinUI / WPF / Electron. For a Windows tray icon,
rasterize a single 16/32px PNG (tray icons aren't auto-tinted — pick black or
white to match the user's taskbar theme).
