import SwiftUI
import EmberKit

/// Reads the menu-only prefs from UserDefaults (replacing menu.json). Defaults
/// come from MenuPrefs.default; validated() guards against stale/unknown values.
/// (The Settings UI that writes these lands in Phase C.)
struct AppStoragePrefs {
    @AppStorage("appIcon") var appIcon = MenuPrefs.default.appIcon
    @AppStorage("trayClaudeGlyph") var trayClaudeGlyph = MenuPrefs.default.trayClaudeGlyph
    @AppStorage("trayCodexGlyph") var trayCodexGlyph = MenuPrefs.default.trayCodexGlyph
    @AppStorage("trayIdleGlyph") var trayIdleGlyph = MenuPrefs.default.trayIdleGlyph

    var menuPrefs: MenuPrefs {
        MenuPrefs(appIcon: appIcon, trayClaudeGlyph: trayClaudeGlyph,
                  trayCodexGlyph: trayCodexGlyph, trayIdleGlyph: trayIdleGlyph).validated()
    }
}
