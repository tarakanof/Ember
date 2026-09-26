import Foundation
import Observation

/// The Settings window's one save status, derived from every config model:
/// shown as the window subtitle.
public enum AggregateSaveStatus: Equatable, Sendable {
    case idle
    case saving
    case saved
    /// At least one model failed; its message shows inline under its section.
    case failed

    /// Errors win over saving, saving over saved.
    public static func combine(_ states: [SaveState]) -> AggregateSaveStatus {
        if states.contains(where: { if case .error = $0 { true } else { false } }) { return .failed }
        if states.contains(.saving) { return .saving }
        if states.contains(.saved) { return .saved }
        return .idle
    }

    /// The window subtitle; nil when there's nothing to say.
    public var subtitle: String? {
        switch self {
        case .idle: nil
        case .saving: "Saving…"
        case .saved: "Saved"
        case .failed: "Couldn't save — see below"
        }
    }
}

/// One config model per settings area, rebuilt when the server changes. Panes
/// bind to these instead of loading and saving by hand.
@MainActor
@Observable
public final class SettingsModels {
    public private(set) var pomodoro: ServerConfigModel<PomoConfig>
    public private(set) var weather: ServerConfigModel<WeatherConfig>
    public private(set) var meetings: ServerConfigModel<MeetingsConfig>
    public private(set) var usage: ServerConfigModel<UsageConfig>
    public private(set) var quiet: ServerConfigModel<QuietConfig>
    /// Server-side display behaviour (`/v1/display/config`).
    public private(set) var display: ServerConfigModel<DisplayConfig>
    /// The Agents pane's producer.env card toggles.
    public let agentsEnv: EnvConfigModel<DisplaySettings>
    /// Source, server URL and source colour in producer.env. The token is
    /// saved explicitly by the Connection pane, never through this model.
    /// Validation runs on save; a rejected value shows as the save error.
    public let connectionEnv: EnvConfigModel<ConnectionSettings>

    /// Every model, for the aggregate status.
    public var all: [any SaveStatusReporting] {
        [pomodoro, weather, meetings, usage, quiet, display, agentsEnv, connectionEnv]
    }

    public var aggregateStatus: AggregateSaveStatus {
        AggregateSaveStatus.combine(all.map(\.status))
    }

    public init(client: APIClient, envPath: URL) {
        (pomodoro, weather, meetings, usage, quiet, display) = Self.serverModels(client)
        agentsEnv = EnvConfigModel(
            envAt: envPath, initial: DisplaySettings(reading: EnvFile(parsing: "")),
            read: { DisplaySettings(reading: $0) },
            apply: { value, env in value.apply(to: &env) })
        connectionEnv = EnvConfigModel(
            envAt: envPath, initial: ConnectionSettings(reading: EnvFile(parsing: "")),
            read: { ConnectionSettings(reading: $0) },
            apply: { value, env in try value.applyTolerant(to: &env, token: nil) })
    }

    /// Points the server-backed models at a new client. Unsaved server edits
    /// are dropped: they were meant for the previous server.
    public func configure(client: APIClient) {
        (pomodoro, weather, meetings, usage, quiet, display) = Self.serverModels(client)
    }

    /// Loads every model (Settings opening, ⌘R).
    public func loadAll() async {
        async let a: Void = pomodoro.load()
        async let b: Void = weather.load()
        async let c: Void = meetings.load()
        async let d: Void = usage.load()
        async let e: Void = quiet.load()
        async let f: Void = display.load()
        async let g: Void = agentsEnv.load()
        async let h: Void = connectionEnv.load()
        _ = await (a, b, c, d, e, f, g, h)
    }

    /// The Focus pane's defaults until the server answers.
    public static let defaultPomoConfig = PomoConfig(
        focusMinutes: 25, shortBreakMinutes: 5, longBreakMinutes: 15,
        roundsBeforeLongBreak: 4, autoStartNext: false, sound: true,
        soundMelody: "", focusColor: "#3aa0ff", breakColor: "#2ee85e",
        maxSessionMinutes: 480)

    private static func serverModels(_ client: APIClient) -> (
        ServerConfigModel<PomoConfig>, ServerConfigModel<WeatherConfig>,
        ServerConfigModel<MeetingsConfig>, ServerConfigModel<UsageConfig>,
        ServerConfigModel<QuietConfig>, ServerConfigModel<DisplayConfig>
    ) {
        let pomo = PomodoroService(client: client)
        let weather = WeatherService(client: client)
        let meetings = MeetingsService(client: client)
        let usage = UsageService(client: client)
        let quiet = QuietService(client: client)
        let display = DisplayService(client: client)
        return (
            ServerConfigModel(initial: defaultPomoConfig,
                              load: { try await pomo.getConfig() }, save: { try await pomo.putConfig($0) }),
            ServerConfigModel(initial: WeatherConfig(),
                              load: { try await weather.getConfig() }, save: { try await weather.putConfig($0) }),
            ServerConfigModel(initial: MeetingsConfig(),
                              load: { try await meetings.getConfig() }, save: { try await meetings.putConfig($0) }),
            ServerConfigModel(initial: UsageConfig(),
                              load: { try await usage.getConfig() }, save: { try await usage.putConfig($0) }),
            ServerConfigModel(initial: QuietConfig(),
                              load: { try await quiet.getConfig() }, save: { try await quiet.putConfig($0) }),
            ServerConfigModel(initial: DisplayConfig(),
                              load: { try await display.getConfig() }, save: { try await display.putConfig($0) })
        )
    }
}
