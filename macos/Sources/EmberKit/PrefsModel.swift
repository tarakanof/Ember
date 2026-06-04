import Foundation

public let appIconPalettes = ["spark", "pixel-e"]
public let trayGlyphs = ["ember", "ember-e", "ember-e-pixel", "claude", "codex", "pomodoro", "coffee"]

/// Friendly label for an app-icon id (used by the App-tab picker).
public func appIconDisplayName(_ id: String) -> String {
    switch id {
    case "spark":   return "Spark"
    case "pixel-e": return "Pixel E"
    default:        return id.capitalized
    }
}

/// Friendly label for a tray-glyph id (used by the App-tab glyph pickers).
public func trayGlyphDisplayName(_ id: String) -> String {
    switch id {
    case "ember":         return "Ember flame"
    case "ember-e":       return "Ember E"
    case "ember-e-pixel": return "Ember E (pixel)"
    case "claude":        return "Claude"
    case "codex":         return "Codex"
    case "pomodoro":      return "Pomodoro"
    case "coffee":        return "Coffee"
    default:              return id.capitalized
    }
}

public struct RGB: Equatable, Sendable { public var r, g, b: UInt8
    public init(r: UInt8, g: UInt8, b: UInt8) { self.r = r; self.g = g; self.b = b }
}

/// Menu-app-local icon prefs (was menu.json; now @AppStorage in the app shell).
public struct MenuPrefs: Equatable, Sendable {
    public var appIcon: String
    public var trayClaudeGlyph: String
    public var trayCodexGlyph: String
    public var trayIdleGlyph: String

    public init(appIcon: String, trayClaudeGlyph: String, trayCodexGlyph: String, trayIdleGlyph: String) {
        self.appIcon = appIcon; self.trayClaudeGlyph = trayClaudeGlyph
        self.trayCodexGlyph = trayCodexGlyph; self.trayIdleGlyph = trayIdleGlyph
    }

    public static let `default` = MenuPrefs(
        appIcon: "spark", trayClaudeGlyph: "claude",
        trayCodexGlyph: "codex", trayIdleGlyph: "ember-e-pixel")

    /// Replaces any unknown value with its default (matches menuprefs.go validate()).
    public func validated() -> MenuPrefs {
        let d = MenuPrefs.default
        return MenuPrefs(
            appIcon: appIconPalettes.contains(appIcon) ? appIcon : d.appIcon,
            trayClaudeGlyph: trayGlyphs.contains(trayClaudeGlyph) ? trayClaudeGlyph : d.trayClaudeGlyph,
            trayCodexGlyph: trayGlyphs.contains(trayCodexGlyph) ? trayCodexGlyph : d.trayCodexGlyph,
            trayIdleGlyph: trayGlyphs.contains(trayIdleGlyph) ? trayIdleGlyph : d.trayIdleGlyph)
    }
}

/// Tray glyph id for the leading tool (ports glyphForTool).
public func glyphForTool(_ tool: String, _ p: MenuPrefs) -> String {
    switch tool {
    case "codex": return p.trayCodexGlyph
    case "claude": return p.trayClaudeGlyph
    default: return p.trayIdleGlyph
    }
}

/// Menu-bar robot colour for a state (ports icon.go stateColor).
public func stateColorRGB(_ state: String) -> RGB {
    switch state {
    case "running": return RGB(r: 0x2e, g: 0xe8, b: 0x5e)
    case "waiting": return RGB(r: 0xff, g: 0xc1, b: 0x4d)
    case "error":   return RGB(r: 0xff, g: 0x3a, b: 0x3a)
    case "done":    return RGB(r: 0x4f, g: 0xa9, b: 0xff)
    default:        return RGB(r: 0x88, g: 0x88, b: 0x88)
    }
}
