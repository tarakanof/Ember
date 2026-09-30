import SwiftUI
import AppKit
import EmberKit

/// The menu-bar icon: the animated bot, or the per-tool glyph recoloured to the
/// current state colour.
///
/// A SwiftUI `Image(...).renderingMode(.template).foregroundStyle(color)` is forced
/// MONOCHROME by the macOS menu bar (the tint is ignored), which dropped the
/// per-state cue. Instead we recolour the glyph into a NON-template `NSImage` with
/// the classic `sourceAtop` recipe (draw the glyph, then paint the colour only
/// where the glyph is opaque), preserving its shape/anti-aliasing. `isTemplate =
/// false` stops the menu bar from re-tinting it. Equivalent to the old Go
/// icon.go `tintAlpha`.
///
/// The icon's colour and the bot's eyes are the only visual state cue, so
/// VoiceOver gets it in words: label "Ember", value the menu's header
/// ("Claude on m4 — Running", "Idle", "Offline"). The value is set on the
/// status item button itself (`StatusItemAccessibility`): `MenuBarExtra`
/// forwards the label but not `accessibilityValue`.
///
/// Equatable on `MenuRows.LabelState` alone, so SwiftUI skips the body on the
/// polls that only move the winner's timestamp or activity text.
struct MenuBarLabel: View, Equatable {
    let state: MenuRows.LabelState

    var body: some View {
        icon
            .accessibilityLabel(Text("Ember"))
            .task(id: state.accessibilityValue) { await StatusItemAccessibility.setValueWhenReady(state.accessibilityValue) }
    }

    private var icon: Image {
        let colored = state.trayTint == "color"
        if state.trayStyle == "bot" {
            // Only the current frame, for the rare re-render (state, glyph,
            // prefs, VoiceOver value); BotAnimator animates the status button directly.
            return Image(nsImage: BotAnimator.shared.menuBarImage(colored: colored))
        }
        return Image(nsImage: Self.trayImage(glyph: state.glyph, state: state.state, colored: colored))
    }

    static func trayImage(glyph: String, state: String, colored: Bool = true) -> NSImage {
        let rgb = stateColorRGB(state)
        let color = NSColor(srgbRed: CGFloat(rgb.r) / 255,
                            green: CGFloat(rgb.g) / 255,
                            blue: CGFloat(rgb.b) / 255,
                            alpha: 1)
        guard let base = NSImage(named: "tray-\(glyph)") else {
            return NSImage()
        }
        if !colored {
            let mono = base.copy() as! NSImage
            mono.isTemplate = true
            return mono
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
