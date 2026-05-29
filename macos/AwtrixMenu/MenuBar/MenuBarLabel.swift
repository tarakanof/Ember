import SwiftUI
import AwtrixMenuKit

/// The menu-bar icon: the tray glyph for the current winning tool, tinted by its
/// state colour. Non-template tint (explicit colour) preserves the per-state cue,
/// matching the old Go menu (it won't auto-adapt to a tinted menu bar — accepted).
struct MenuBarLabel: View {
    let session: Session?
    let prefs: MenuPrefs

    private var glyph: String { glyphForTool(session?.tool ?? "", prefs) }
    private var color: Color { Color(stateColorRGB(session?.state ?? "idle")) }

    var body: some View {
        Image("tray-\(glyph)")
            .renderingMode(.template)
            .foregroundStyle(color)
    }
}
