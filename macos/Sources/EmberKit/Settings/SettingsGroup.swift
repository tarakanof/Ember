import Foundation

public enum SettingsGroup: String, CaseIterable, Sendable {
    case knobStatus = "knob.status"
    case knobFirmware = "knob.firmware"
    case knobDiagnostics = "knob.diagnostics"
    case knobCrashDumps = "knob.crash-dumps"
    case knobDisplay = "knob.display"
    case knobBehavior = "knob.behavior"
    case knobAdvanced = "knob.advanced"

    public static let storageKey = "settings.collapsed"

    public func isCollapsed(in stored: String) -> Bool {
        SettingsTree.expandedSet(stored).contains(rawValue)
    }

    public func setCollapsed(_ collapsed: Bool, in stored: String) -> String {
        var set = SettingsTree.expandedSet(stored)
        if collapsed { set.insert(rawValue) } else { set.remove(rawValue) }
        return SettingsTree.storedExpanded(set)
    }
}
