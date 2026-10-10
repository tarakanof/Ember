import Foundation
import Observation

@MainActor
@Observable
public final class ClockConfigModel: SaveStatusReporting {
    public private(set) var deviceID: String?
    public private(set) var config: ConfigModel<ClockConfig>
    public private(set) var missing = false

    public var isActive: Bool { deviceID != nil && !missing }
    public var status: SaveState { isActive ? config.status : .idle }

    @ObservationIgnored public var onSaved: (@MainActor (_ saved: ClockConfig, _ previous: ClockConfig?) -> Void)? {
        didSet { config.onSaved = onSaved }
    }

    @ObservationIgnored public weak var legacy: SettingsModels?
    @ObservationIgnored private var client: APIClient
    @ObservationIgnored private var lastProbe: Date?
    @ObservationIgnored private let probeBackoff: TimeInterval
    @ObservationIgnored private let now: @Sendable () -> Date
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void

    public convenience init(client: APIClient) {
        self.init(client: client, debounce: .milliseconds(600), sleep: { try await Task.sleep(for: $0) })
    }

    init(client: APIClient, debounce: Duration, sleep: @escaping @Sendable (Duration) async throws -> Void,
         probeBackoff: TimeInterval = 300, now: @escaping @Sendable () -> Date = { Date() }) {
        self.client = client
        self.probeBackoff = probeBackoff
        self.now = now
        self.debounce = debounce
        self.sleep = sleep
        config = ConfigModel(initial: ClockConfig(), load: { throw APIError.notConfigured },
                             saveChange: { _, _ in }, debounce: debounce, savedHold: .seconds(2), sleep: sleep)
    }

    public func configure(client next: APIClient, deviceID id: String?) {
        let sameServer = ServerIdentity(next) == ServerIdentity(client)
        guard !sameServer || id != deviceID else {
            if missing, lastProbe.map({ now().timeIntervalSince($0) >= probeBackoff }) ?? true {
                Task { await load() }
            }
            return
        }
        if sameServer, config.hasUnsavedChanges {
            let pendingEdit = config
            Task { await pendingEdit.saveNow() }
        } else {
            config.cancelPendingSave()
        }
        client = next
        lastProbe = nil
        deviceID = id
        missing = false
        generation += 1
        config = makeModel(id: id)
        if id != nil { Task { await load() } }
    }

    public func load() async {
        guard deviceID != nil else { return }
        let gen = generation
        let model = config
        if missing {
            lastProbe = now()
            guard await model.fetch(), gen == generation else { return }
            missing = false
            return
        }
        await model.load()
        guard gen == generation, model.loadError == .featureOff, !model.isLoaded else { return }
        missing = true
    }

    public func writeRotation(_ update: AppsUpdate) async throws {
        guard let id = deviceID, !missing else { throw APIError.notConfigured }
        let gen = generation
        let body = JSONValue.object(["rotation": .object([
            "order": .array(update.order.map(JSONValue.string)),
            "disabled": .array(update.disabled.map(JSONValue.string)),
        ])])
        do {
            let _: ClockConfig = try await client.request("PUT", Self.path(id), body: body, budget: .clock)
        } catch {
            if Self.isMissing(error) { markMissing(gen) }
            throw Self.facadeFailure(error) ?? error
        }
    }

    private func mark() -> ClockLegacyMark? { legacy?.clockMark() }

    private func adopt(_ saved: ClockConfig, previous: ClockConfig?, since mark: ClockLegacyMark?) {
        guard let mark else { return }
        legacy?.adopt(saved, previous: previous, since: mark)
    }

    private func replay(_ sent: ClockConfig, previous: ClockConfig?, since mark: ClockLegacyMark?, gen: Int) {
        let unsaved = gen == generation ? config.draft : sent
        if gen == generation { config.revert() }
        if let mark { legacy?.adopt(unsaved, previous: previous, since: mark, resave: true) }
        markMissing(gen)
    }

    nonisolated static func path(_ id: String) -> String { "/v1/devices/\(KnobService.escape(id))/config" }

    nonisolated static func isMissing(_ error: Error) -> Bool {
        if case .http(404, _)? = error as? APIError { return true }
        return false
    }

    nonisolated static func facadeFailure(_ error: Error) -> FeedError? {
        switch error as? APIError {
        case .http(503, _)?:
            .rejected(LocalizedStringResource(
                "The clock was busy with another change, so this one wasn't saved. Showing the current settings.",
                comment: "Error: the server answered 503 to a clock settings change (another change held the clock); nothing was saved and the app reloaded the settings."))
        case .http(502, _)?:
            .rejected(LocalizedStringResource(
                "The clock didn't confirm the change, so it may be only partly saved. Showing the current settings.",
                comment: "Error: the server answered 502 to a clock settings change (the clock write was cut short); the app reloaded the settings."))
        default:
            nil
        }
    }

    private func markMissing(_ gen: Int) {
        guard gen == generation else { return }
        missing = true
    }

    private func makeModel(id: String?) -> ConfigModel<ClockConfig> {
        let client = self.client
        let gen = generation
        let model = ConfigModel<ClockConfig>(
            initial: ClockConfig(),
            load: {
                guard let id else { throw APIError.notConfigured }
                return try await client.get(Self.path(id), budget: .clock)
            },
            saveChange: { [weak self] value, previous in
                guard let id else { throw APIError.notConfigured }
                let patch = value.patch(from: previous ?? value)
                guard !patch.isEmpty else { return }
                let mark = await self?.mark()
                do {
                    let _: ClockConfig = try await client.request("PUT", Self.path(id), body: JSONValue.object(patch),
                                                                  budget: .clock)
                } catch {
                    if Self.isMissing(error) {
                        await self?.replay(value, previous: previous, since: mark, gen: gen)
                        throw error
                    }
                    guard let cause = Self.facadeFailure(error) else { throw error }
                    guard let current: ClockConfig = try? await client.get(Self.path(id), budget: .clock) else { throw cause }
                    throw SaveRecovered(current: current, cause: cause) { sent, draft in
                        draft.rebased(onto: current, from: sent)
                    }
                }
                await self?.adopt(value, previous: previous, since: mark)
            },
            debounce: debounce, savedHold: .seconds(2), sleep: sleep)
        model.onSaved = onSaved
        return model
    }
}

@MainActor
public struct ClockAppLens<Slice: ClockAppSlice> {
    public let source: ConfigModel<Slice.Source>
    public let clock: ClockConfigModel
    public let slice: WritableKeyPath<ClockConfig.Apps, Slice>

    public init(source: ConfigModel<Slice.Source>, clock: ClockConfigModel, slice: WritableKeyPath<ClockConfig.Apps, Slice>) {
        self.source = source
        self.clock = clock
        self.slice = slice
    }

    public var usesFacade: Bool { clock.isActive }

    public var draft: Slice.Source {
        get {
            var d = source.draft
            if usesFacade { clock.config.draft.apps[keyPath: slice].apply(to: &d) }
            return d
        }
        nonmutating set {
            if usesFacade {
                var next = clock.config.draft.apps[keyPath: slice]
                next.take(from: newValue)
                if next != clock.config.draft.apps[keyPath: slice] { clock.config.draft.apps[keyPath: slice] = next }
            } else {
                source.draft = newValue
            }
        }
    }

    public var isLoaded: Bool { source.isLoaded && (!usesFacade || clock.config.isLoaded) }

    public var loadError: FeedError? {
        if usesFacade, !clock.config.isLoaded, let e = clock.config.loadError { return e }
        return source.loadError
    }

    public var saveError: FeedError? { usesFacade ? clock.config.saveError : source.saveError }

    public func load() async {
        async let a: Void = source.load()
        async let b: Void = clock.load()
        _ = await (a, b)
    }
}
