import Foundation
import Observation

/// One-off knob writes whose failure is shown next to their control.
public enum KnobAction: Hashable, Sendable {
    case rename, rotate, forget, factoryReset
}

/// What a plugged-in knob-like board said when probed.
public enum KnobPortStatus: Equatable, Sendable {
    case probing
    case cinder(KnobIdentity)
    /// No Improv answer: an ESP32 board not running cinder.
    case notCinder
    /// Couldn't open the port (busy in another app, gone).
    case unavailable
}

/// Settings › Knob: the single registered knob (`/v1/devices`), its
/// settings (autosaved, merge PUT), the one-off actions, and the knob-like
/// boards on USB.
@MainActor
@Observable
public final class KnobModel {
    public private(set) var knob: KnobDevice?
    /// Every registered knob; the pane shows only `knob`.
    public private(set) var devices: [KnobDevice] = []
    public private(set) var isLoaded = false
    public private(set) var loadError: FeedError?
    public private(set) var settings: ServerConfigModel<KnobSettings>
    public private(set) var running: Set<KnobAction> = []
    public private(set) var actionErrors: [KnobAction: FeedError] = [:]
    /// A rotation was asked for in this session (the server says so too,
    /// until the knob collects the token).
    public var rotationPending: Bool { knob?.rotationPending ?? false }
    public private(set) var portStatus: [String: KnobPortStatus] = [:]

    public let ports: KnobSerialPorts
    @ObservationIgnored public private(set) var service: KnobService
    @ObservationIgnored public let opener: any KnobLinkOpener
    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    /// The probe running now; at most one, so two opens never race.
    @ObservationIgnored private var probeTask: Task<Void, Never>?
    /// Set while the setup sheet owns the port, so probes don't open it.
    @ObservationIgnored public var portBusy = false
    @ObservationIgnored var probeTimeouts = KnobProvisioner.Timeouts()

    public var all: [any SaveStatusReporting] { [settings] }

    public convenience init(service: KnobService, ports: KnobSerialPorts = KnobSerialPorts(),
                            opener: any KnobLinkOpener = SerialPortOpener()) {
        self.init(service: service, ports: ports, opener: opener, debounce: .milliseconds(600),
                  sleep: { try await Task.sleep(for: $0) })
    }

    init(service: KnobService, ports: KnobSerialPorts, opener: any KnobLinkOpener, debounce: Duration,
         sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.service = service
        self.ports = ports
        self.opener = opener
        self.debounce = debounce
        self.sleep = sleep
        settings = Self.settingsModel(service, id: nil, debounce: debounce, sleep: sleep)
    }

    /// Points the model at another server.
    public func configure(service next: KnobService) {
        guard next != service else { return }
        settings.cancelPendingSave()
        service = next
        knob = nil; devices = []; isLoaded = false; loadError = nil; actionErrors = [:]
        settings = Self.settingsModel(next, id: nil, debounce: debounce, sleep: sleep)
        Task { await load() }
    }

    /// Reads the registry, then the knob's settings.
    public func load() async {
        do {
            let list = try await service.devices()
            devices = list
            apply(KnobDevice.current(in: list))
            isLoaded = true
            loadError = nil
        } catch {
            loadError = FeedError(error)
            return
        }
        if knob != nil { await settings.load() }
    }

    private func apply(_ next: KnobDevice?) {
        if next?.id != knob?.id {
            settings.cancelPendingSave()
            settings = Self.settingsModel(service, id: next?.id, debounce: debounce, sleep: sleep)
        }
        knob = next
    }

    /// Applies an edit and keeps the result valid (floor ≤ level, home on).
    public func edit(_ change: (inout KnobSettings) -> Void) {
        var d = settings.draft
        change(&d)
        settings.draft = d.normalized()
    }

    /// Sets the knob's diagnostics level and saves at once (the Dashboard
    /// has no autosave); false when there is no knob or the save failed.
    @discardableResult
    public func setDiagnostics(_ level: KnobDiagnostics) async -> Bool {
        guard knob != nil else { return false }
        if !settings.isLoaded { await settings.load() }
        guard settings.isLoaded else { return false }
        edit { $0.diagnostics = level }
        await settings.saveNow()
        return settings.saveError == nil && settings.applied?.diagnostics == level
    }

    // MARK: Actions

    @discardableResult
    func perform(_ action: KnobAction, _ body: () async throws -> Void) async -> Bool {
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

    public func rename(_ name: String) async {
        guard let id = knob?.id else { return }
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed != knob?.name else { return }
        await perform(.rename) {
            let view = try await self.service.rename(id: id, name: trimmed)
            if self.knob?.id == view.id { self.knob = view }
        }
    }

    public func rotate() async {
        guard let id = knob?.id else { return }
        if await perform(.rotate, { try await self.service.rotate(id: id) }) { await load() }
    }

    /// Revokes the token and forgets the knob.
    @discardableResult
    public func forget() async -> Bool {
        guard let id = knob?.id else { return false }
        let ok = await perform(.forget) { try await self.service.forget(id: id) }
        if ok { await load() }
        return ok
    }

    /// After a setup: forgets the knob it replaced, then reloads.
    public func didSetUp(_ device: KnobDevice, replacing old: KnobDevice?) async {
        if let old, old.id != device.id { try? await service.forget(id: old.id) }
        await load()
    }

    public func clearError(_ action: KnobAction) { actionErrors[action] = nil }

    // MARK: USB

    /// The plugged-in board that is this knob (same `hw_id`), else any
    /// knob-like board.
    public var connectedPort: KnobSerialPort? {
        let candidates = ports.ports
        if let hw = knob?.hwID, let p = candidates.first(where: { portHwID($0) == hw }) { return p }
        return candidates.first
    }

    /// The registered knob is on USB right now.
    public var registeredKnobOnUSB: Bool {
        guard let hw = knob?.hwID else { return false }
        return ports.ports.contains { portHwID($0) == hw }
    }

    private func portHwID(_ p: KnobSerialPort) -> String? {
        if case .cinder(let id)? = portStatus[p.path], let hw = id.hwID { return hw }
        return p.hwID
    }

    /// Asks the plugged-in boards who they are (Improv device info, then
    /// closes). Only call it while Settings › Knob is on screen or the user
    /// acts: the port is shared with idf.py monitor and esptool (download
    /// mode is `303a:1001` too), so Ember never opens it on its own.
    /// `retryFailed` probes again boards that weren't cinder or were busy.
    public func probePorts(retryFailed: Bool = false) async {
        await waitForProbe()
        guard !portBusy else { return }
        let current = Set(ports.ports.map(\.path))
        portStatus = portStatus.filter { current.contains($0.key) }
        let todo = ports.ports.filter { p in
            switch portStatus[p.path] {
            case nil, .probing?: true
            case .cinder?: false
            case .notCinder?, .unavailable?: retryFailed
            }
        }
        guard !todo.isEmpty else { return }
        let task = Task { @MainActor in
            for port in todo where !self.portBusy && !Task.isCancelled {
                self.portStatus[port.path] = .probing
                let status = await self.probe(port)
                if self.ports.ports.contains(port) { self.portStatus[port.path] = status }
            }
        }
        probeTask = task
        await task.value
        if probeTask == task { probeTask = nil }
    }

    /// Waits for a probe in flight (the setup sheet calls this before it
    /// opens the port).
    public func waitForProbe() async {
        while let t = probeTask {
            await t.value
            if probeTask == t { probeTask = nil }
        }
    }

    /// Erases the knob on `port` over USB: Wi-Fi, Ember settings, token. It
    /// reboots to its setup face; the registry record stays until forgotten.
    @discardableResult
    public func factoryReset(port: KnobSerialPort) async -> Bool {
        let provisioner = KnobProvisioner(opener: opener, service: service)
        await waitForProbe()
        portBusy = true
        defer { portBusy = false }
        let ok = await perform(.factoryReset) {
            let (session, _) = try await provisioner.connect(path: port.path, usbHwID: port.hwID)
            do {
                try await provisioner.factoryReset(session)
            } catch {
                await session.close()
                throw error
            }
            await session.close()
        }
        if ok { portStatus[port.path] = nil }
        return ok
    }

    /// Records what the setup sheet learned, so the pane doesn't probe again.
    public func record(_ status: KnobPortStatus, for port: KnobSerialPort) {
        portStatus[port.path] = status
    }

    private func probe(_ port: KnobSerialPort) async -> KnobPortStatus {
        let provisioner = KnobProvisioner(opener: opener, service: service, timeouts: probeTimeouts)
        do {
            let (session, identity) = try await provisioner.connect(path: port.path, usbHwID: port.hwID)
            await session.close()
            return .cinder(identity)
        } catch KnobSetupError.notCinder {
            return .notCinder
        } catch {
            return .unavailable
        }
    }

    // MARK: Settings model

    private static func settingsModel(_ svc: KnobService, id: String?, debounce: Duration,
                                      sleep: @escaping @Sendable (Duration) async throws -> Void)
        -> ServerConfigModel<KnobSettings> {
        let base = KnobAppliedRef()
        let model = ServerConfigModel<KnobSettings>(
            initial: .defaults,
            load: {
                guard let id else { throw APIError.http(status: 404, body: "no knob") }
                return try await svc.config(id: id)
            },
            save: { next in
                guard let id else { return }
                let patch = next.patch(from: await base.applied())
                if !patch.isEmpty { try await svc.updateConfig(id: id, patch: patch) }
            },
            debounce: debounce, savedHold: .seconds(2), sleep: sleep)
        base.model = model
        return model
    }
}

@MainActor
private final class KnobAppliedRef {
    weak var model: ServerConfigModel<KnobSettings>?
    func applied() -> KnobSettings { model?.applied ?? .defaults }
}
