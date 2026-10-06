import Foundation

public struct ClaudeHookRegistration: Sendable, Equatable {
    public enum Source: Sendable, Equatable {
        case plugin
        case settings
        case both
        case none
    }

    public static let pluginID = "ember@ember"

    public let pluginEnabled: Bool
    public let settingsEvents: Int
    public let killSwitch: Bool
    public let settingsUnreadable: Bool

    public init(pluginEnabled: Bool, settingsEvents: Int, killSwitch: Bool, settingsUnreadable: Bool = false) {
        self.pluginEnabled = pluginEnabled
        self.settingsEvents = settingsEvents
        self.killSwitch = killSwitch
        self.settingsUnreadable = settingsUnreadable
    }

    public var source: Source {
        switch (pluginEnabled, settingsEvents > 0) {
        case (true, true): .both
        case (true, false): .plugin
        case (false, true): .settings
        case (false, false): .none
        }
    }

    public static func read(settingsJSON: Data?, killSwitch: Bool) -> ClaudeHookRegistration {
        guard let data = settingsJSON else {
            return ClaudeHookRegistration(pluginEnabled: false, settingsEvents: 0, killSwitch: killSwitch)
        }
        let root = data.starts(with: [0xEF, 0xBB, 0xBF]) ? nil
            : (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        return ClaudeHookRegistration(pluginEnabled: pluginEnabled(root ?? [:]),
                                      settingsEvents: producerHookEvents(root ?? [:]),
                                      killSwitch: killSwitch, settingsUnreadable: root == nil)
    }

    public static func read(home: URL, readFile: (String) -> Data?, fileExists: (String) -> Bool) -> ClaudeHookRegistration {
        read(settingsJSON: readFile(home.appendingPathComponent(".claude/settings.json").path),
             killSwitch: fileExists(home.appendingPathComponent(killSwitchRelPath).path))
    }

    public static let killSwitchRelPath = ".config/ember/claude-hooks.disabled"

    private static let producerNames = ["ember-claude-producer", "awtrix-claude-producer"]

    private static func pluginEnabled(_ root: [String: Any]) -> Bool {
        guard let plugins = root["enabledPlugins"] as? [String: Any],
              let value = plugins[pluginID] as? NSNumber else { return false }
        return CFGetTypeID(value) == CFBooleanGetTypeID() && value.boolValue
    }

    private static func producerHookEvents(_ root: [String: Any]) -> Int {
        guard let hooks = root["hooks"] as? [String: Any] else { return 0 }
        return hooks.values.filter { entries in
            (entries as? [Any] ?? []).contains(where: entryMatchesProducer)
        }.count
    }

    private static func entryMatchesProducer(_ entry: Any) -> Bool {
        guard let hooks = (entry as? [String: Any])?["hooks"] as? [Any] else { return false }
        return hooks.contains { hook in
            guard let command = (hook as? [String: Any])?["command"] as? String else { return false }
            return producerNames.contains { command.contains($0) }
        }
    }
}

public enum ClaudeHooksNotice: Sendable, Equatable {
    case fine
    case paused
    case registeredTwice
    case missing
    case settingsUnreadable

    public var offersConfigure: Bool { self != .fine && self != .settingsUnreadable }

    public static func notice(for registration: ClaudeHookRegistration, reportingOn: Bool) -> ClaudeHooksNotice {
        guard reportingOn else { return .fine }
        if registration.settingsUnreadable { return .settingsUnreadable }
        if registration.killSwitch { return .paused }
        switch registration.source {
        case .both: return .registeredTwice
        case .none: return .missing
        case .plugin, .settings: return .fine
        }
    }
}
