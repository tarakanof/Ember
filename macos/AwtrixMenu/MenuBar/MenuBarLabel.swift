import SwiftUI
import AppKit
import AwtrixMenuKit

/// The menu-bar icon: the per-tool glyph recoloured to the current state colour.
///
/// A SwiftUI `Image(...).renderingMode(.template).foregroundStyle(color)` is forced
/// MONOCHROME by the macOS menu bar (the tint is ignored), which dropped the
/// per-state cue. Instead we recolour the glyph into a NON-template `NSImage` with
/// the classic `sourceAtop` recipe (draw the glyph, then paint the colour only
/// where the glyph is opaque), preserving its shape/anti-aliasing. `isTemplate =
/// false` stops the menu bar from re-tinting it. Equivalent to the old Go
/// icon.go `tintAlpha`.
struct MenuBarLabel: View {
    let session: Session?
    let prefs: MenuPrefs

    var body: some View {
        Image(nsImage: Self.trayImage(tool: session?.tool ?? "",
                                      state: session?.state ?? "idle",
                                      prefs: prefs))
    }

    static func trayImage(tool: String, state: String, prefs: MenuPrefs) -> NSImage {
        let rgb = stateColorRGB(state)
        let color = NSColor(srgbRed: CGFloat(rgb.r) / 255,
                            green: CGFloat(rgb.g) / 255,
                            blue: CGFloat(rgb.b) / 255,
                            alpha: 1)
        guard let base = NSImage(named: "tray-\(glyphForTool(tool, prefs))") else {
            return NSImage()
        }
        let size = base.size == .zero ? NSSize(width: 18, height: 18) : base.size
        let rect = NSRect(origin: .zero, size: size)

        let out = NSImage(size: size)
        out.lockFocus()
        base.draw(at: .zero, from: rect, operation: .sourceOver, fraction: 1)
        color.set()
        rect.fill(using: .sourceAtop)
        out.unlockFocus()
        out.isTemplate = false
        return out
    }
}
