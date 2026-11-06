import SwiftUI
import EmberKit

enum EmberColors {
    static func state(_ state: Session.State) -> Color { Color(state.color) }

    static func hex(_ hex: String, fallback: Color = .accentColor) -> Color {
        RGB(hex: hex).map(Color.init) ?? fallback
    }

    static func phase(_ phase: PomoPhase, config: PomoConfig?) -> Color {
        guard let config else { return phase.isBreak ? .green : .accentColor }
        return phase.isBreak ? hex(config.breakColor, fallback: .green) : hex(config.focusColor)
    }

    static let heat = Gradient(colors: [
        Color.blue.opacity(0.08), Color.blue.opacity(0.35), Color.blue.opacity(0.65), Color.blue,
    ])
}
