import Foundation
import Observation

/// Save status for writes that aren't a config model: app visibility and
/// order, clock buttons. Feeds the Settings window's one status.
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

    /// Runs one write, reporting saving → saved (for 2 s) or the error.
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

/// What the Sounds pane can do with the clock's audio routes.
public enum AudioAvailability: Equatable, Sendable {
    /// Not asked yet.
    case unknown
    case available
    /// The clock has no buzzer (the server answered 503).
    case noOutput
    /// The server predates the audio routes (404).
    case unsupported
}

/// One-off Settings writes whose failure is shown next to their button.
/// Restart and display power are clock actions (`ActionRunner`), shared with
/// the menu and the Dashboard.
public enum DeviceAction: Hashable, Sendable {
    case testChime, stopAudio, useClock, discover, buttons, apps
}

/// Everything the Clock and Sounds panes edit on the clock, proxied through
/// the server's `/v1/device/*`: the settings (saved as a patch of the keys
/// that changed), the overlay, sensor offsets, native apps, buttons, and the
/// read-only catalogue (capabilities, melodies, address). Loads sequentially:
/// fanning these requests out used to empty the server's per-IP token bucket.
@MainActor
@Observable
public final class DeviceSettingsModel {
    public private(set) var settings: ServerConfigModel<DeviceSettings>
    /// The ambient overlay (`/v1/device/display`).
    public private(set) var display: ServerConfigModel<DeviceDisplay>
    public private(set) var sensors: ServerConfigModel<SensorCalibration>
    /// Apps and buttons writes.
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
    /// Why the last one-off action failed, per action; cleared by its next success.
    public private(set) var actionErrors: [DeviceAction: FeedError] = [:]
    public private(set) var running: Set<DeviceAction> = []

    /// The settings model loaded; the rest of the pane follows it.
    public var isLoaded: Bool { settings.isLoaded }
    public var loadError: FeedError? { settings.loadError }
    public var all: [any SaveStatusReporting] { [settings, display, sensors, writes] }
    /// The server writes mute, buzzer volume and inherit colours.
    public var supportsNG11: Bool { settings.applied?.serverSupportsNG11 ?? false }
    /// Unknown counts as yes, so a clock that hasn't answered still shows the
    /// sound controls.
    public var hasBuzzer: Bool { capabilities?.hasBuzzer ?? true }
    /// The server has the NG 1.1 control routes (display power, audio): the
    /// audio route answered, with or without a buzzer. 0.27.x has neither,
    /// though its display GET relays NG's `power`.
    public var supportsControlRoutes: Bool { audio == .available || audio == .noOutput }
    /// The server is new enough but the clock's firmware predates NG 1.1.
    public var firmwareTooOld: Bool { isLoaded && supportsControlRoutes && !supportsNG11 }
    public var nativeApps: [AppInfo] { NativeAppsPlan.listed(apps) }
    /// The transition names the clock reports, or a fallback list.
    public var transitions: [String] {
        let live = capabilities?.transitions ?? []
        return live.isEmpty ? DeviceKnownValues.fallbackTransitions : live
    }
    public var overlays: [String] {
        let live = capabilities?.overlays ?? []
        return live.isEmpty ? OverlayEffect.allCases.map(\.rawValue) : live
    }

    @ObservationIgnored public private(set) var service: DeviceService
    /// Holds the one display-power value; each overlay read reports the
    /// `power` NG's display GET carries to it.
    @ObservationIgnored private weak var live: LiveModel?
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let now: @Sendable () -> Date
    @ObservationIgnored private var lastFullLoad: Date?
    /// A forced load asked for while another was running; it runs next.
    @ObservationIgnored private var forcedLoadQueued = false
    /// Secondary data (apps, buttons, catalogue) is refetched at most this often
    /// when the window regains focus; ⌘R (`load(force: true)`) forces it.
    static let secondaryRefresh: TimeInterval = 30

    /// `live` receives the display power each overlay read sees.
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

    /// Points the model at another server. A no-op for the same server and
    /// token (defensive: `ServerConnection.reload` is the authoritative
    /// check); otherwise pending edits are dropped and everything reloads.
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

    /// Loads the settings, then (when they loaded) the rest one request at a
    /// time. `force` refetches the secondary data even if it's fresh; a
    /// forced load asked for while another runs is queued, never dropped.
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
            // Its own task: the caller's may be the cancelled one.
            Task { await self.load(force: true) }
        }
    }

    private func loadOnce(force: Bool) async {
        await settings.load()
        guard settings.isLoaded, settings.loadError == nil else {
            // The address is what you need when the clock can't be reached.
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
        // A load cut short (pane switched) fetched nothing useful; don't let
        // it hold off the next one.
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

    // MARK: Native apps

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

    // MARK: Buttons

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

    // MARK: One-off actions

    /// Runs an action and records its failure; returns whether it worked.
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

    /// Saves the chosen clock on the server, then reloads from it.
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
        // A save diffs against what the model last accepted (`applied`), not
        // against the last response: a load that arrives while an edit is
        // pending is discarded, and diffing against it would send its values
        // (the Pomodoro takeover's) back as stale edits.
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
                // Ticketed before the request: a read from a previous server,
                // or one a power write overtook, can't win.
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

/// The settings model's accepted value, for the patch diff.
@MainActor
private final class AppliedRef {
    weak var model: ServerConfigModel<DeviceSettings>?
    func applied() -> DeviceSettings { model?.applied ?? DeviceSettings() }
}
