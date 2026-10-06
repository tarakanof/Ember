import Foundation
import Observation

@MainActor
@Observable
public final class WriteStatus: SaveStatusReporting {
    public private(set) var status: SaveState = .idle
    @ObservationIgnored private var reset: Task<Void, Never>?
    @ObservationIgnored private let hold: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void

    init(hold: Duration = .seconds(2), sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.hold = hold
        self.sleep = sleep
    }

    func run(_ write: () async throws -> Void) async -> FeedError? {
        reset?.cancel()
        status = .saving
        do {
            try await write()
            status = .saved
            let hold = self.hold
            reset = Task { [weak self] in
                do { try await self?.sleep(hold) } catch { return }
                guard let self, !Task.isCancelled, self.status == .saved else { return }
                self.status = .idle
            }
            return nil
        } catch {
            let e = FeedError(error)
            status = .error(String(localized: e.saveMessage))
            return e
        }
    }

    func clear() {
        reset?.cancel()
        status = .idle
    }
}

public enum AudioAvailability: Equatable, Sendable {
    case unknown
    case available
    case noOutput
    case unsupported
}

public enum DeviceAction: Hashable, Sendable {
    case testChime, stopAudio, useClock, discover, buttons, apps
}

@MainActor
@Observable
public final class DeviceSettingsModel {
    public private(set) var settings: ServerConfigModel<DeviceSettings>
    public private(set) var display: ServerConfigModel<DeviceDisplay>
    public private(set) var sensors: ServerConfigModel<SensorCalibration>
    public let writes: WriteStatus

    public private(set) var capabilities: DeviceCapabilities?
    public private(set) var config: DeviceConfig?
    public private(set) var apps: [AppInfo] = []
    public private(set) var buttons: ButtonStatus?
    public private(set) var stats: DeviceStats?
    public private(set) var melodies: [DeviceMelody] = []
    public private(set) var audio: AudioAvailability = .unknown
    public private(set) var discovered: [DiscoveredClock]?
    public private(set) var isLoading = false
    public private(set) var actionErrors: [DeviceAction: FeedError] = [:]
    public private(set) var running: Set<DeviceAction> = []

    public var isLoaded: Bool { settings.isLoaded }
    public var loadError: FeedError? { settings.loadError }
    public var all: [any SaveStatusReporting] { [settings, display, sensors, writes] }
    public var supportsNG11: Bool { settings.applied?.serverSupportsNG11 ?? false }
    public var hasBuzzer: Bool { capabilities?.hasBuzzer ?? true }
    public var supportsControlRoutes: Bool { audio == .available || audio == .noOutput }
    public var firmwareTooOld: Bool { isLoaded && supportsControlRoutes && !supportsNG11 }
    public var nativeApps: [AppInfo] { NativeAppsPlan.listed(apps) }
    public var transitions: [String] {
        let live = capabilities?.transitions ?? []
        return live.isEmpty ? DeviceKnownValues.fallbackTransitions : live
    }
    public var overlays: [String] {
        let live = capabilities?.overlays ?? []
        return live.isEmpty ? OverlayEffect.allCases.map(\.rawValue) : live
    }

    @ObservationIgnored public private(set) var service: DeviceService
    @ObservationIgnored private weak var live: LiveModel?
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let now: @Sendable () -> Date
    @ObservationIgnored private var lastFullLoad: Date?
    @ObservationIgnored private var forcedLoadQueued = false
    static let secondaryRefresh: TimeInterval = 30

    public convenience init(service: DeviceService, live: LiveModel?) {
        self.init(service: service, live: live, debounce: .milliseconds(600),
                  sleep: { try await Task.sleep(for: $0) }, now: { Date() })
    }

    init(service: DeviceService, live: LiveModel? = nil, debounce: Duration,
         sleep: @escaping @Sendable (Duration) async throws -> Void,
         now: @escaping @Sendable () -> Date) {
        self.service = service
        self.live = live
        self.sleep = sleep
        self.debounce = debounce
        self.now = now
        writes = WriteStatus(sleep: sleep)
        (settings, display, sensors) = Self.models(service, live: live, debounce: debounce, sleep: sleep)
    }

    public func configure(service next: DeviceService) {
        guard next != service else { return }
        for m in [settings as any PendingSaveCancelling, display, sensors] { m.cancelPendingSave() }
        service = next
        (settings, display, sensors) = Self.models(next, live: live, debounce: debounce, sleep: sleep)
        capabilities = nil; config = nil; apps = []; buttons = nil; stats = nil
        melodies = []; audio = .unknown; discovered = nil
        actionErrors = [:]; lastFullLoad = nil
        writes.clear()
        Task { await load(force: true) }
    }

    public func load(force: Bool = false) async {
        guard !isLoading else {
            if force { forcedLoadQueued = true }
            return
        }
        isLoading = true
        await loadOnce(force: force)
        isLoading = false
        if forcedLoadQueued {
            forcedLoadQueued = false
            Task { await self.load(force: true) }
        }
    }

    private func loadOnce(force: Bool) async {
        await settings.load()
        guard settings.isLoaded, settings.loadError == nil else {
            if config == nil { config = try? await service.config() }
            return
        }
        if !force, let last = lastFullLoad, now().timeIntervalSince(last) < Self.secondaryRefresh { return }
        await display.load()
        await sensors.load()
        let svc = service
        config = (try? await svc.config()) ?? config
        capabilities = (try? await svc.capabilities()) ?? capabilities
        if let a = try? await svc.apps() { apps = a }
        buttons = (try? await svc.buttons()) ?? buttons
        stats = (try? await svc.stats()) ?? stats
        await loadMelodies()
        if !Task.isCancelled { lastFullLoad = now() }
    }

    private func loadMelodies() async {
        do {
            melodies = try await service.melodies().melodies.filter(\.valid)
            audio = .available
        } catch {
            switch FeedError(error) {
            case .featureOff: audio = .unsupported
            case .server where (error as? APIError).map(Self.isUnavailable) ?? false: audio = .noOutput
            default: break
            }
        }
    }

    private static func isUnavailable(_ e: APIError) -> Bool {
        if case .http(503, _) = e { return true }
        return false
    }

    public func setApp(_ name: String, enabled: Bool) async {
        let before = apps
        let update = NativeAppsPlan.toggle(name, enabled: enabled, in: apps)
        apps = apps.map { a in var a = a; if a.name == name { a.enabled = enabled }; return a }
        await write(.apps, rollback: { self.apps = before }) { try await self.service.updateApps(update) }
    }

    public func moveApps(fromOffsets source: IndexSet, toOffset destination: Int) async {
        let before = apps
        let (next, update) = NativeAppsPlan.move(in: apps, fromOffsets: source, toOffset: destination)
        guard next != apps else { return }
        apps = next
        await write(.apps, rollback: { self.apps = before }) { try await self.service.updateApps(update) }
    }

    public func setButtonsEnabled(_ on: Bool) async {
        await write(.buttons, rollback: {}) {
            let status = try await self.service.updateButtons(enabled: on)
            self.buttons = status
        }
    }

    private func write(_ action: DeviceAction, rollback: () -> Void, _ body: () async throws -> Void) async {
        if let e = await writes.run(body) {
            rollback()
            actionErrors[action] = e
        } else {
            actionErrors[action] = nil
        }
    }

    @discardableResult
    public func perform(_ action: DeviceAction, _ body: @escaping () async throws -> Void) async -> Bool {
        running.insert(action)
        defer { running.remove(action) }
        do {
            try await body()
            actionErrors[action] = nil
            return true
        } catch {
            actionErrors[action] = FeedError(error)
            return false
        }
    }

    public func playTestChime(melody: String? = nil) async {
        await perform(.testChime) { try await self.service.playTestChime(melody: melody) }
    }

    public func stopAudio() async {
        await perform(.stopAudio) { try await self.service.stopAudio() }
    }

    public func discover() async {
        await perform(.discover) {
            let result = try await self.service.discover()
            self.discovered = result.candidates
        }
    }

    public func use(_ clock: DiscoveredClock) async {
        if await perform(.useClock, { try await self.service.setConfig(baseURL: clock.baseURL) }) {
            discovered = nil
            settings.revert()
            await load(force: true)
        }
    }

    private static func models(_ svc: DeviceService, live: LiveModel?, debounce: Duration,
                               sleep: @escaping @Sendable (Duration) async throws -> Void) -> (
        ServerConfigModel<DeviceSettings>, ServerConfigModel<DeviceDisplay>, ServerConfigModel<SensorCalibration>
    ) {
        let base = AppliedRef()
        let settings = ServerConfigModel<DeviceSettings>(
            initial: DeviceSettings(),
            load: { try await svc.settings() },
            save: { next in
                let patch = next.patch(from: await base.applied())
                if !patch.isEmpty { try await svc.update(patch: patch) }
            },
            debounce: debounce, savedHold: .seconds(2), sleep: sleep)
        base.model = settings
        let display = ServerConfigModel<DeviceDisplay>(
            initial: DeviceDisplay(),
            load: {
                let ticket = await live?.displayPowerTicket()
                let d = try await svc.display()
                if let on = d.power, let ticket { await live?.reportDisplayPower(on, read: ticket) }
                return d
            },
            save: { try await svc.updateDisplay($0) },
            debounce: debounce, savedHold: .seconds(2), sleep: sleep)
        let sensors = ServerConfigModel<SensorCalibration>(
            initial: SensorCalibration(),
            load: { try await svc.sensors() },
            save: { try await svc.updateSensors($0) },
            debounce: debounce, savedHold: .seconds(2), sleep: sleep)
        return (settings, display, sensors)
    }
}

@MainActor
private final class AppliedRef {
    weak var model: ServerConfigModel<DeviceSettings>?
    func applied() -> DeviceSettings { model?.applied ?? DeviceSettings() }
}
