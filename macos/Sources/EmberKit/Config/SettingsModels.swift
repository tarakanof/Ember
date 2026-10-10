import Foundation
import Observation

public enum AggregateSaveStatus: Equatable, Sendable {
    case idle
    case saving
    case saved
    case failed

    public static func combine(_ states: [SaveState]) -> AggregateSaveStatus {
        if states.contains(where: { if case .error = $0 { true } else { false } }) { return .failed }
        if states.contains(.saving) { return .saving }
        if states.contains(.saved) { return .saved }
        return .idle
    }

    public var subtitle: LocalizedStringResource? {
        switch self {
        case .idle: nil
        case .saving: "Saving…"
        case .saved: "Saved"
        case .failed: "Couldn't save — see below"
        }
    }
}

@MainActor
@Observable
public final class SettingsModels {
    public private(set) var pomodoro: ServerConfigModel<PomoConfig>
    public private(set) var weather: ServerConfigModel<WeatherConfig>
    public private(set) var meetings: ServerConfigModel<MeetingsConfig>
    public private(set) var usage: ServerConfigModel<UsageConfig>
    public private(set) var quiet: ServerConfigModel<QuietConfig>
    public private(set) var display: ServerConfigModel<DisplayConfig>
    public let agentsEnv: EnvConfigModel<DisplaySettings>
    public let connectionEnv: EnvConfigModel<ConnectionSettings>
    public let producerTuning: EnvConfigModel<ProducerTuning>

    public var all: [any SaveStatusReporting] {
        [pomodoro, weather, meetings, usage, quiet, display, agentsEnv, connectionEnv, producerTuning]
    }

    public var aggregateStatus: AggregateSaveStatus {
        AggregateSaveStatus.combine(all.map(\.status))
    }

    @ObservationIgnored private var server: ServerIdentity
    @ObservationIgnored public var onClockSourceSaved: (@MainActor () -> Void)?

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
        producerTuning = Self.producerTuningModel(envStore)
        watchClockSources()
    }

    private func watchClockSources() {
        let notify: @MainActor () -> Void = { [weak self] in self?.onClockSourceSaved?() }
        usage.onSaved = { _, _ in notify() }
        pomodoro.onSaved = { _, _ in notify() }
        weather.onSaved = { _, _ in notify() }
        meetings.onSaved = { _, _ in notify() }
    }

    static func producerTuningModel(_ store: EnvFileStore) -> EnvConfigModel<ProducerTuning> {
        EnvConfigModel(
            env: store, initial: ProducerTuning(reading: EnvFile(parsing: "")),
            read: { ProducerTuning(reading: $0) },
            applyChange: { value, previous, env in try value.apply(to: &env, from: previous) })
    }

    @discardableResult
    public func configure(client: APIClient) -> Bool {
        let next = ServerIdentity(client)
        guard next != server else { return false }
        server = next
        for m in [pomodoro, weather, meetings, usage, quiet, display] as [any PendingSaveCancelling] {
            m.cancelPendingSave()
        }
        (pomodoro, weather, meetings, usage, quiet, display) = Self.serverModels(client)
        watchClockSources()
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

    public func clockMark() -> ClockLegacyMark {
        ClockLegacyMark(usage: .init(usage), pomodoro: .init(pomodoro), weather: .init(weather), meetings: .init(meetings))
    }

    public func adopt(_ saved: ClockConfig, previous: ClockConfig?, since mark: ClockLegacyMark, resave: Bool = false) {
        let apps = saved.apps, before = previous?.apps
        Self.adopt(apps.agents, before?.agents, into: usage, slot: mark.usage, resave: resave)
        Self.adopt(apps.focus, before?.focus, into: pomodoro, slot: mark.pomodoro, resave: resave)
        Self.adopt(apps.weather, before?.weather, into: weather, slot: mark.weather, resave: resave)
        Self.adopt(apps.calendar, before?.calendar, into: meetings, slot: mark.meetings, resave: resave)
    }

    private static func adopt<S: ClockAppSlice>(_ slice: S, _ before: S?, into model: ConfigModel<S.Source>,
                                                slot: ClockLegacyMark.Slot, resave: Bool) {
        guard slice != before, slot.model === model else { return }
        model.amend(overlapped: resave || slot.overlapped(model)) { source in
            if let before { slice.apply(to: &source, changedFrom: before) } else { slice.apply(to: &source) }
        }
    }

    public func loadAll() async {
        async let server: Void = loadServerModels()
        async let g: Void = agentsEnv.load()
        async let h: Void = connectionEnv.load()
        async let i: Void = producerTuning.load()
        _ = await (server, g, h, i)
    }

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

    private static func remote<T: Codable & Equatable & Sendable>(
        _ client: APIClient, _ path: String, initial: T
    ) -> ServerConfigModel<T> {
        ServerConfigModel(initial: initial,
                          load: { try await client.get(path) },
                          save: { try await client.put(path, body: $0) })
    }
}

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

public struct ClockLegacyMark: Sendable {
    struct Slot: @unchecked Sendable {
        weak var model: AnyObject?
        let epoch: Int
        let saving: Bool

        @MainActor init<T>(_ m: ConfigModel<T>) {
            model = m
            epoch = m.saveEpoch
            saving = m.isSaving
        }

        @MainActor func overlapped<T>(_ m: ConfigModel<T>) -> Bool {
            saving || m.isSaving || m.saveEpoch != epoch
        }
    }

    let usage: Slot
    let pomodoro: Slot
    let weather: Slot
    let meetings: Slot
}
