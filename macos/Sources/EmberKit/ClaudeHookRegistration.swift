import Foundation

public struct ClaudeHookRegistration: Sendable, Equatable {
    public enum Source: Sendable, Equatable {
        case plugin
        case settings
        case both
        case none
    }

    public static let pluginID = "ember@ember"
    public static let modPluginID = "ember-mod@ember"
    public static let sourcePlaceholder = "set-me-to-this-laptop-id"

    public let pluginEnabled: Bool
    public let settingsEvents: Int
    public let killSwitch: Bool
    public let settingsUnreadable: Bool
    public let modEnabled: Bool
    public let modSource: String?

    public init(pluginEnabled: Bool, settingsEvents: Int, killSwitch: Bool, settingsUnreadable: Bool = false,
                modEnabled: Bool = false, modSource: String? = nil) {
        self.pluginEnabled = pluginEnabled
        self.settingsEvents = settingsEvents
        self.killSwitch = killSwitch
        self.settingsUnreadable = settingsUnreadable
        self.modEnabled = modEnabled
        self.modSource = modSource
    }

    public static func resolveModSource(env: EnvFile) -> String? {
        let explicit = env.get("EMBER_MOD_SOURCE").trimmingCharacters(in: .whitespacesAndNewlines)
        if !explicit.isEmpty { return explicit }
        let base = env.get(SettingsKeys.source).trimmingCharacters(in: .whitespacesAndNewlines)
        if base.isEmpty || base.lowercased() == sourcePlaceholder { return nil }
        return base + "-mod"
    }

    public func withModSource(_ source: String?) -> ClaudeHookRegistration {
        ClaudeHookRegistration(pluginEnabled: pluginEnabled, settingsEvents: settingsEvents, killSwitch: killSwitch,
                               settingsUnreadable: settingsUnreadable, modEnabled: modEnabled, modSource: source)
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
        return ClaudeHookRegistration(pluginEnabled: pluginEnabled(root ?? [:], id: pluginID),
                                      settingsEvents: producerHookEvents(root ?? [:]),
                                      killSwitch: killSwitch, settingsUnreadable: root == nil,
                                      modEnabled: pluginEnabled(root ?? [:], id: modPluginID))
    }

    public static func read(home: URL, readFile: (String) -> Data?, fileExists: (String) -> Bool) -> ClaudeHookRegistration {
        read(settingsJSON: readFile(home.appendingPathComponent(".claude/settings.json").path),
             killSwitch: fileExists(home.appendingPathComponent(killSwitchRelPath).path))
    }

    public static let killSwitchRelPath = ".config/ember/claude-hooks.disabled"

    private static let producerNames = ["ember-claude-producer", "awtrix-claude-producer"]

    private static func pluginEnabled(_ root: [String: Any], id: String) -> Bool {
        guard let plugins = root["enabledPlugins"] as? [String: Any],
              let value = plugins[id] as? NSNumber else { return false }
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
        case .none: return registration.modEnabled ? .fine : .missing
        case .plugin, .settings: return .fine
        }
    }
}
