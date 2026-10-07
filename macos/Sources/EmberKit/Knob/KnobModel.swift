import Foundation
import Observation

public enum KnobAction: Hashable, Sendable {
    case rename, rotate, forget, factoryReset, coredump, deleteCoredump
}

public enum KnobPortStatus: Equatable, Sendable {
    case probing
    case cinder(KnobIdentity)
    case notCinder
    case unavailable
}

@MainActor
@Observable
public final class KnobModel {
    public private(set) var knob: KnobDevice?
    public private(set) var devices: [KnobDevice] = []
    public private(set) var isLoaded = false
    public private(set) var loadError: FeedError?
    public private(set) var settings: ServerConfigModel<KnobSettings>
    public private(set) var running: Set<KnobAction> = []
    public private(set) var actionErrors: [KnobAction: FeedError] = [:]
    public private(set) var coredumps: [KnobCoredump] = []
    public private(set) var coredumpsLoaded = false
    public var rotationPending: Bool { knob?.rotationPending ?? false }
    public private(set) var portStatus: [String: KnobPortStatus] = [:]

    public let ports: KnobSerialPorts
    public let ota: KnobOTAModel
    @ObservationIgnored public private(set) var service: KnobService
    @ObservationIgnored public let opener: any KnobLinkOpener
    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var probeTask: Task<Void, Never>?
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
        ota = KnobOTAModel(service: service)
        settings = Self.settingsModel(service, id: nil, debounce: debounce, sleep: sleep)
    }

    public func configure(service next: KnobService) {
        guard next != service else { return }
        settings.cancelPendingSave()
        service = next
        knob = nil; devices = []; coredumps = []; coredumpsLoaded = false; isLoaded = false; loadError = nil; actionErrors = [:]
        settings = Self.settingsModel(next, id: nil, debounce: debounce, sleep: sleep)
        ota.configure(service: next, device: nil)
        Task { await load() }
    }

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
        if knob != nil {
            await settings.load()
            await loadCoredumps()
            await ota.loadStatus()
            await ota.loadImages()
        }
    }

    public func loadCoredumps() async {
        guard let id = knob?.id else { coredumps = []; coredumpsLoaded = false; return }
        guard let list = try? await service.coredumps(id: id), knob?.id == id else { return }
        coredumps = list
        coredumpsLoaded = true
    }

    public func coredump(for crash: KnobCrash) -> KnobCoredump? {
        guard let id = crash.id else { return nil }
        return coredumps.first { $0.id == id }
    }

    @discardableResult
    public func deleteCoredump(_ dump: KnobCoredump) async -> Bool {
        guard let id = knob?.id else { return false }
        let ok = await perform(.deleteCoredump) {
            do {
                try await self.service.deleteCoredump(id: id, dump: dump.id)
            } catch let APIError.http(status, _) where status == 404 {}
        }
        await loadCoredumps()
        return ok
    }

    public func coredumpData(_ dump: KnobCoredump) async -> Data? {
        guard let id = knob?.id else { return nil }
        var data: Data?
        await perform(.coredump) { data = try await self.service.coredump(id: id, dump: dump.id) }
        return data
    }

    private func apply(_ next: KnobDevice?) {
        if next?.id != knob?.id {
            settings.cancelPendingSave()
            settings = Self.settingsModel(service, id: next?.id, debounce: debounce, sleep: sleep)
            coredumps = []
            coredumpsLoaded = false
        }
        ota.configure(service: service, device: next?.id)
        knob = next
    }

    public func edit(_ change: (inout KnobSettings) -> Void) {
        var d = settings.draft
        change(&d)
        settings.draft = d.normalized()
    }

    @discardableResult
    public func setDiagnostics(_ level: KnobDiagnostics) async -> Bool {
        guard knob != nil else { return false }
        if !settings.isLoaded { await settings.load() }
        guard settings.isLoaded else { return false }
        edit { $0.diagnostics = level }
        await settings.saveNow()
        return settings.saveError == nil && settings.applied?.diagnostics == level
    }

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

    @discardableResult
    public func forget() async -> Bool {
        guard let id = knob?.id else { return false }
        let ok = await perform(.forget) { try await self.service.forget(id: id) }
        if ok { await load() }
        return ok
    }

    public func didSetUp(_ device: KnobDevice, replacing old: KnobDevice?) async {
        if let old, old.id != device.id { try? await service.forget(id: old.id) }
        await load()
    }

    public func clearError(_ action: KnobAction) { actionErrors[action] = nil }

    public var connectedPort: KnobSerialPort? {
        let candidates = ports.ports
        if let hw = knob?.hwID, let p = candidates.first(where: { portHwID($0) == hw }) { return p }
        return candidates.first
    }

    public var registeredKnobOnUSB: Bool {
        guard let hw = knob?.hwID else { return false }
        return ports.ports.contains { portHwID($0) == hw }
    }

    private func portHwID(_ p: KnobSerialPort) -> String? {
        if case .cinder(let id)? = portStatus[p.path], let hw = id.hwID { return hw }
        return p.hwID
    }

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

    public func waitForProbe() async {
        while let t = probeTask {
            await t.value
            if probeTask == t { probeTask = nil }
        }
    }

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
