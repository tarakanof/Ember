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
    public var subtitle: LocalizedStringResource? {
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

    @ObservationIgnored private var server: ServerIdentity

    public init(client: APIClient, envStore: EnvFileStore) {
        server = ServerIdentity(client)
        (pomodoro, weather, meetings, usage, quiet, display) = Self.serverModels(client)
        agentsEnv = EnvConfigModel(
            env: envStore, initial: DisplaySettings(reading: EnvFile(parsing: "")),
            read: { DisplaySettings(reading: $0) },
            apply: { value, env in value.apply(to: &env) })
        connectionEnv = EnvConfigModel(
            env: envStore, initial: ConnectionSettings(reading: EnvFile(parsing: "")),
            read: { ConnectionSettings(reading: $0) },
            apply: { value, env in try value.applyTolerant(to: &env, token: nil) })
    }

    /// Points the server-backed models at a new client. A no-op when the URL
    /// and token are unchanged (a source name or colour save). Otherwise
    /// pending server edits are dropped (they were meant for the previous
    /// server) and the fresh models load at once. Returns whether it swapped.
    @discardableResult
    public func configure(client: APIClient) -> Bool {
        let next = ServerIdentity(client)
        // Defensive: `ServerConnection.reload` already decides when to call.
        guard next != server else { return false }
        server = next
        for m in [pomodoro, weather, meetings, usage, quiet, display] as [any PendingSaveCancelling] {
            m.cancelPendingSave()
        }
        (pomodoro, weather, meetings, usage, quiet, display) = Self.serverModels(client)
        Task { await self.loadServerModels() }
        return true
    }

    private func loadServerModels() async {
        async let a: Void = pomodoro.load()
        async let b: Void = weather.load()
        async let c: Void = meetings.load()
        async let d: Void = usage.load()
        async let e: Void = quiet.load()
        async let f: Void = display.load()
        _ = await (a, b, c, d, e, f)
    }

    /// Loads every model (Settings opening, ⌘R while Settings is open).
    public func loadAll() async {
        async let server: Void = loadServerModels()
        async let g: Void = agentsEnv.load()
        async let h: Void = connectionEnv.load()
        _ = await (server, g, h)
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
        (
            remote(client, "/v1/pomodoro/config", initial: defaultPomoConfig),
            remote(client, "/v1/weather/config", initial: WeatherConfig()),
            remote(client, "/v1/meetings/config", initial: MeetingsConfig()),
            remote(client, "/v1/usage/config", initial: UsageConfig()),
            remote(client, "/v1/quiet/config", initial: QuietConfig()),
            remote(client, "/v1/display/config", initial: DisplayConfig())
        )
    }

    /// A config the server serves as `GET` and replaces as `PUT` on one path.
    private static func remote<T: Codable & Equatable & Sendable>(
        _ client: APIClient, _ path: String, initial: T
    ) -> ServerConfigModel<T> {
        ServerConfigModel(initial: initial,
                          load: { try await client.get(path) },
                          save: { try await client.put(path, body: $0) })
    }
}

/// Which server a client talks to, as far as settings and feeds care.
struct ServerIdentity: Equatable, Sendable {
    let baseURL: URL?
    let token: String?
    init(_ client: APIClient) {
        baseURL = client.baseURL
        token = client.token
    }
}

@MainActor
protocol PendingSaveCancelling {
    func cancelPendingSave()
}

extension ConfigModel: PendingSaveCancelling {}
