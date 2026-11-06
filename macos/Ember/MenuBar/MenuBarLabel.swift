import SwiftUI
import AppKit
import EmberKit

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
            return Image(nsImage: BotAnimator.shared.liveMenuBarImage(colored: colored))
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
