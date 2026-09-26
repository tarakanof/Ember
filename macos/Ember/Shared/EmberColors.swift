import SwiftUI
import EmberKit

/// The app's few non-semantic colours. Everything else uses semantic colours
/// (`.primary`, `.secondary`, `.red`…).
enum EmberColors {
    /// A session state's colour, shared with the menu-bar icon and the bot.
    static func state(_ state: Session.State) -> Color { Color(state.color) }

    /// A "#RRGGBB" config colour (Pomodoro focus/break), or `fallback` when
    /// the string doesn't parse.
    static func hex(_ hex: String, fallback: Color = .accentColor) -> Color {
        RGB(hex: hex).map(Color.init) ?? fallback
    }

    /// The phase colour from the Pomodoro config: focus colour for focus,
    /// break colour for breaks.
    static func phase(_ phase: PomoPhase, config: PomoConfig?) -> Color {
        guard let config else { return phase.isBreak ? .green : .accentColor }
        return phase.isBreak ? hex(config.breakColor, fallback: .green) : hex(config.focusColor)
    }

    /// The one sequential ramp for heat maps: light to deep blue.
    static let heat = Gradient(colors: [
        Color.blue.opacity(0.08), Color.blue.opacity(0.35), Color.blue.opacity(0.65), Color.blue,
    ])
}
