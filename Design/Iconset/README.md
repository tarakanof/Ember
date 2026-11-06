# Awtrix Manager — Icon Package

Everything you need for the macOS app icon and the menu-bar items, in every
size macOS asks for. Four palettes: `multicolor-rgb` (default), `cyan-green`,
`warm-amber`, `aurora`.

```
export/
├─ awtrix-icon-<palette>-1024.png        1024 master PNGs (transparent corners)
│
├─ macos-app-icon/                         ◀ THE APP ICON, all sizes
│   ├─ AwtrixIcon-<palette>.icns           ready-to-use .icns (no iconutil needed)
│   └─ <palette>.iconset/                  the PNG size ladder (see @2x note)
│         icon_16x16.png        16
│         icon_16x16-2x.png     32   (rename → @2x)
│         icon_32x32.png        32
│         icon_32x32-2x.png     64   (rename → @2x)
│         icon_128x128.png      128
│         icon_128x128-2x.png   256  (rename → @2x)
│         icon_256x256.png      256
│         icon_256x256-2x.png   512  (rename → @2x)
│         icon_512x512.png      512
│         icon_512x512-2x.png   1024 (rename → @2x)
│
├─ menubar/                                ◀ MENU-BAR template glyphs
│   ├─ png/  <name>-template.png   (18) + <name>-template-2x.png (36 → @2x)
│   └─ svg/  <name>-template.svg
│
└─ svg/  awtrix-icon-<palette>-{glow,flat}.svg   vector app mark (in-app UI)
```

## App icon — fastest path

Just use the matching **`.icns`**:

- Drop `AwtrixIcon-<palette>.icns` into your bundle and point
  `CFBundleIconFile` at it, **or**
- In Xcode, drag the 10 PNGs from `<palette>.iconset/` into the `AppIcon` slot
  of `Assets.xcassets` (after the @2x rename below).

### The @2x rename (only needed for the .iconset route)

This sandbox can't write `@` in filenames, so retina files are `-2x`. macOS /
`iconutil` need `@2x`. One line, run inside a `.iconset` folder:

```bash
for f in *-2x.png; do mv "$f" "${f/-2x/@2x}"; done
# then, if you want to (re)build the icns yourself:
cd .. && iconutil -c icns multicolor-rgb.iconset -o AwtrixIcon.icns
```

(The provided `.icns` files are already built, so this is optional.)

## Menu-bar glyphs

Monochrome **template images** — set `isTemplate` and macOS tints them:

```swift
let img = NSImage(contentsOfFile: "awtrix-template.png")!
img.isTemplate = true
statusItem.button?.image = img
```

Glyphs: `awtrix` / `awtrix-screen` (default A/A2), `aicode` / `aicode-chat`
(Claude Code), `code` / `code-hex` (Codex), `pomodoro`. Same `-2x → @2x`
rename applies to the retina PNGs.

> The `aicode` / `code` glyphs are **original** marks — not the Anthropic /
> OpenAI logos, which are trademarked. Swap in licensed brand art if you have it.

## Windows (later)

App icon: `magick awtrix-icon-<palette>-1024.png -define icon:auto-resize=256,128,64,48,32,16 awtrix.ico`
In-app: the SVGs work directly in WinUI / WPF / Electron.
