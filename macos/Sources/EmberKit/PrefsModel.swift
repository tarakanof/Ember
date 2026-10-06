import Foundation

public let appIconPalettes = ["bot", "spark", "pixel-e"]
public let trayStyles = ["bot", "glyphs"]
public let trayTints = ["color", "mono"]
public let trayGlyphs = ["ember", "ember-e", "ember-e-pixel", "claude", "codex", "pomodoro", "coffee"]

public func appIconDisplayName(_ id: String) -> String {
    switch id {
    case "bot":     return "Bot (animated)"
    case "spark":   return "Spark"
    case "pixel-e": return "Pixel E"
    default:        return id.capitalized
    }
}

public func trayStyleDisplayName(_ id: String) -> String {
    id == "bot" ? "Animated bot" : "Tool glyphs"
}

public func trayTintDisplayName(_ id: String) -> String {
    id == "mono" ? "Monochrome" : "Colored"
}

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

public struct MenuPrefs: Equatable, Sendable {
    public var appIcon: String
    public var trayClaudeGlyph: String
    public var trayCodexGlyph: String
    public var trayIdleGlyph: String
    public var trayStyle: String
    public var trayTint: String

    public init(appIcon: String, trayClaudeGlyph: String, trayCodexGlyph: String, trayIdleGlyph: String,
                trayStyle: String = "bot", trayTint: String = "color") {
        self.appIcon = appIcon; self.trayClaudeGlyph = trayClaudeGlyph
        self.trayCodexGlyph = trayCodexGlyph; self.trayIdleGlyph = trayIdleGlyph
        self.trayStyle = trayStyle; self.trayTint = trayTint
    }

    public static let `default` = MenuPrefs(
        appIcon: "bot", trayClaudeGlyph: "claude",
        trayCodexGlyph: "codex", trayIdleGlyph: "ember-e-pixel", trayStyle: "bot", trayTint: "color")

    public func validated() -> MenuPrefs {
        let d = MenuPrefs.default
        return MenuPrefs(
            appIcon: appIconPalettes.contains(appIcon) ? appIcon : d.appIcon,
            trayClaudeGlyph: trayGlyphs.contains(trayClaudeGlyph) ? trayClaudeGlyph : d.trayClaudeGlyph,
            trayCodexGlyph: trayGlyphs.contains(trayCodexGlyph) ? trayCodexGlyph : d.trayCodexGlyph,
            trayIdleGlyph: trayGlyphs.contains(trayIdleGlyph) ? trayIdleGlyph : d.trayIdleGlyph,
            trayStyle: trayStyles.contains(trayStyle) ? trayStyle : d.trayStyle,
            trayTint: trayTints.contains(trayTint) ? trayTint : d.trayTint)
    }
}

public func glyphForTool(_ tool: String, _ p: MenuPrefs) -> String {
    switch tool {
    case "codex": return p.trayCodexGlyph
    case "claude": return p.trayClaudeGlyph
    default: return p.trayIdleGlyph
    }
}

public func stateColorRGB(_ state: String) -> RGB {
    switch state {
    case "running": return RGB(r: 0x2e, g: 0xe8, b: 0x5e)
    case "waiting": return RGB(r: 0xff, g: 0xc1, b: 0x4d)
    case "error":   return RGB(r: 0xff, g: 0x3a, b: 0x3a)
    case "done":    return RGB(r: 0x4f, g: 0xa9, b: 0xff)
    default:        return RGB(r: 0x88, g: 0x88, b: 0x88)
    }
}
