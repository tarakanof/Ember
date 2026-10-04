import Foundation

/// Where the Claude producer's hooks are registered, read the way
/// `ember-claude-producer doctor` reads it (Go `hookRegistrationSources` and
/// `hooksEnabledAt` in `cmd/ember-claude-producer/plugin.go`).
public struct ClaudeHookRegistration: Sendable, Equatable {
    /// Which configuration registers the hooks.
    public enum Source: Sendable, Equatable {
        /// The `ember@ember` plugin is enabled in `~/.claude/settings.json`.
        case plugin
        /// `~/.claude/settings.json` carries the producer's hooks.
        case settings
        /// Both, so Claude Code runs every hook twice and each event POSTs twice.
        case both
        /// Neither, so Claude Code sessions don't report.
        case none
    }

    /// The Claude Code plugin id Ember's hooks ship under.
    public static let pluginID = "ember@ember"

    /// Whether `enabledPlugins["ember@ember"]` is JSON `true`.
    public let pluginEnabled: Bool
    /// How many hook events in settings.json carry a producer command.
    public let settingsEvents: Int
    /// Whether the kill switch `~/.config/ember/claude-hooks.disabled` exists,
    /// which makes every registered hook exit without reporting.
    public let killSwitch: Bool

    public init(pluginEnabled: Bool, settingsEvents: Int, killSwitch: Bool) {
        self.pluginEnabled = pluginEnabled
        self.settingsEvents = settingsEvents
        self.killSwitch = killSwitch
    }

    /// The registration derived from the plugin flag and the settings events.
    public var source: Source {
        switch (pluginEnabled, settingsEvents > 0) {
        case (true, true): .both
        case (true, false): .plugin
        case (false, true): .settings
        case (false, false): .none
        }
    }

    /// Parses `~/.claude/settings.json` (nil or unparsable reads as empty,
    /// like the Go side) plus the kill switch's presence.
    public static func read(settingsJSON: Data?, killSwitch: Bool) -> ClaudeHookRegistration {
        let root = settingsJSON.flatMap { try? JSONSerialization.jsonObject(with: $0) } as? [String: Any] ?? [:]
        return ClaudeHookRegistration(pluginEnabled: pluginEnabled(root),
                                      settingsEvents: producerHookEvents(root),
                                      killSwitch: killSwitch)
    }

    /// Reads the registration under `home` through the given file accessors.
    public static func read(home: URL, readFile: (String) -> Data?, fileExists: (String) -> Bool) -> ClaudeHookRegistration {
        read(settingsJSON: readFile(home.appendingPathComponent(".claude/settings.json").path),
             killSwitch: fileExists(home.appendingPathComponent(killSwitchRelPath).path))
    }

    /// The kill switch's path under `$HOME` (Go `hooksDisabledPath`).
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

/// What Settings › Agents says about the Claude hooks next to the Claude row.
public enum ClaudeHooksNotice: Sendable, Equatable {
    /// Nothing to fix.
    case fine
    /// Reporting is on, but the kill switch keeps every hook silent.
    case paused
    /// The plugin and settings.json both register the hooks: double POSTs.
    case registeredTwice
    /// Reporting is on, but no hooks are registered.
    case missing

    /// Whether running the helper's `configure` fixes it.
    public var offersConfigure: Bool { self != .fine }

    /// The notice for a registration, given whether Claude reporting is on.
    /// With reporting off nothing is offered: configure removes the kill
    /// switch, which would turn the hooks back on.
    public static func notice(for registration: ClaudeHookRegistration, reportingOn: Bool) -> ClaudeHooksNotice {
        guard reportingOn else { return .fine }
        if registration.killSwitch { return .paused }
        switch registration.source {
        case .both: return .registeredTwice
        case .none: return .missing
        case .plugin, .settings: return .fine
        }
    }
}
