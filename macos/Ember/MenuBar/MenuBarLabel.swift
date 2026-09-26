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
struct MenuBarLabel: View {
    let session: Session?
    let connection: ConnectionHealth
    let prefs: MenuPrefs

    private var bot = BotAnimator.shared

    init(session: Session?, connection: ConnectionHealth, prefs: MenuPrefs) {
        self.session = session
        self.connection = connection
        self.prefs = prefs
    }

    var body: some View {
        icon
            .accessibilityLabel(Text("Ember"))
            .task(id: accessibilityValue) { await StatusItemAccessibility.setValueWhenReady(accessibilityValue) }
    }

    private var accessibilityValue: String {
        String(localized: MenuRows.accessibilityValue(connection: connection, winning: session))
    }

    private var icon: Image {
        let colored = prefs.trayTint == "color"
        if prefs.trayStyle == "bot" {
            return Image(nsImage: bot.menuBarImage(colored: colored))
        }
        return Image(nsImage: Self.trayImage(tool: session?.tool ?? "",
                                             state: session?.state ?? "idle",
                                             prefs: prefs, colored: colored))
    }

    static func trayImage(tool: String, state: String, prefs: MenuPrefs, colored: Bool = true) -> NSImage {
        let rgb = stateColorRGB(state)
        let color = NSColor(srgbRed: CGFloat(rgb.r) / 255,
                            green: CGFloat(rgb.g) / 255,
                            blue: CGFloat(rgb.b) / 255,
                            alpha: 1)
        guard let base = NSImage(named: "tray-\(glyphForTool(tool, prefs))") else {
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
