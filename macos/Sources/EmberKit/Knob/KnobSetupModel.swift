import Foundation
import Observation

/// The USB setup sheet: connect, the knob's own Wi-Fi scan, Ember URL and
/// name, then the provisioning progress.
@MainActor
@Observable
public final class KnobSetupModel {
    public enum Mode: Equatable, Sendable {
        /// Wi-Fi + Ember + token.
        case setup
        /// Wi-Fi only (Advanced › Change Wi-Fi).
        case changeWiFi
    }

    public enum Stage: Equatable, Sendable {
        case waitingForKnob
        case connecting
        case notCinder
        /// Knob identified; the form is live.
        case ready
        case sending(KnobSetupPhase)
        case done
        case failed(KnobSetupError)
    }

    public let mode: Mode
    public private(set) var stage: Stage = .waitingForKnob
    public private(set) var port: KnobSerialPort?
    public private(set) var identity: KnobIdentity?
    public private(set) var networks: [KnobWiFiNetwork] = []
    public private(set) var isScanning = false
    public private(set) var scanFailed = false
    public private(set) var device: KnobDevice?

    public var ssid = ""
    public var password = ""
    public var emberURL = ""
    public var name = ""
    /// The server host the URL had before it became a LAN address.
    public private(set) var replacedHost: String?

    @ObservationIgnored private let provisioner: KnobProvisioner
    @ObservationIgnored private var session: KnobSession?
    @ObservationIgnored private var preferredSSID: String?
    @ObservationIgnored private var suggestedURL: String?
    @ObservationIgnored private var work: Task<Void, Never>?

    public init(mode: Mode, provisioner: KnobProvisioner, emberURL: KnobEmberURL.Suggestion?,
                name: String, preferredSSID: String?) {
        self.mode = mode
        self.provisioner = provisioner
        self.emberURL = emberURL?.url ?? ""
        suggestedURL = emberURL?.url
        replacedHost = emberURL?.replacedHost
        self.name = name
        self.preferredSSID = preferredSSID
    }

    /// Uses the server URL as the knob will reach it, unless already edited.
    public func suggest(_ url: KnobEmberURL.Suggestion?) {
        guard let url, emberURL.isEmpty || emberURL == suggestedURL else { return }
        emberURL = url.url
        suggestedURL = url.url
        replacedHost = url.replacedHost
    }

    /// Preselects this Mac's network once the knob's scan has it.
    public func prefer(ssid: String?) {
        preferredSSID = ssid
        if let ssid, networks.contains(where: { $0.ssid == ssid }), self.ssid.isEmpty || self.ssid == networks.first?.ssid {
            self.ssid = ssid
        }
    }

    /// The knob the server has registered now (Settings › Knob's one knob).
    @ObservationIgnored public var registered: @MainActor () -> KnobDevice? = { nil }

    /// The registered knob this setup would replace: a different board.
    public var replaces: KnobDevice? {
        guard mode == .setup, let current = registered(), let hw = identity?.hwID, hw != current.hwID else { return nil }
        return current
    }

    public var canSend: Bool {
        guard stage == .ready, !ssid.isEmpty else { return false }
        switch networks.first(where: { $0.ssid == ssid })?.secured {
        case true?: if !Self.isValidWPAPassword(password) { return false }
        case false?: break
        // Typed by hand: open (no password) or a valid WPA one.
        case nil: if !password.isEmpty && !Self.isValidWPAPassword(password) { return false }
        }
        if mode == .setup {
            return CinderLineCodec.isValidEmberURL(emberURL)
                && !name.trimmingCharacters(in: .whitespaces).isEmpty
        }
        return true
    }

    public var isBusy: Bool {
        switch stage {
        case .connecting, .sending: true
        default: false
        }
    }

    /// The host the suggested URL replaced, while the URL is still the
    /// suggestion.
    public var replacedHostNote: String? { emberURL == suggestedURL ? replacedHost : nil }

    /// A password is typed but can't be a WPA one (8–63 characters).
    public var passwordInvalid: Bool { !password.isEmpty && !Self.isValidWPAPassword(password) }

    public var emberURLValid: Bool { CinderLineCodec.isValidEmberURL(emberURL) }

    /// Connects to `port` (nil: wait for one to be plugged in).
    public func attach(_ port: KnobSerialPort?) {
        guard let port else {
            if session == nil, !isBusy, stage != .done { stage = .waitingForKnob }
            return
        }
        guard self.port != port || stage == .waitingForKnob || stage == .notCinder else { return }
        if case .sending = stage { return }
        if stage == .done { return }
        self.port = port
        connect()
    }

    public func connect() {
        guard let port else { return }
        work?.cancel()
        stage = .connecting
        let p = provisioner
        work = Task {
            if let s = session { await s.close(); session = nil }
            do {
                let (s, id) = try await p.connect(path: port.path, usbHwID: port.hwID)
                session = s
                identity = id
                if name.isEmpty { name = id.info.name }
                stage = .ready
                await scan()
            } catch KnobSetupError.notCinder {
                stage = .notCinder
            } catch let e as KnobSetupError {
                stage = .failed(e)
            } catch {
                stage = .failed(.disconnected)
            }
        }
    }

    public func scan() async {
        guard let session, !isScanning else { return }
        isScanning = true
        scanFailed = false
        defer { isScanning = false }
        do {
            networks = try await provisioner.scan(session)
            if ssid.isEmpty || !networks.contains(where: { $0.ssid == ssid }) {
                ssid = networks.first { $0.ssid == preferredSSID }?.ssid ?? networks.first?.ssid ?? ssid
            }
        } catch {
            scanFailed = true
        }
    }

    /// Runs the setup; `finished` gets the registered device and the knob it
    /// replaces, captured now: once the mint lands, the new record is the
    /// newest and `replaces` would read nil.
    public func send(finished: @escaping @MainActor (KnobDevice, KnobDevice?) async -> Void) {
        guard canSend, let session, let identity else { return }
        let old = replaces
        let request = KnobSetupRequest(ssid: ssid, password: password, emberURL: emberURL,
                                       name: name.trimmingCharacters(in: .whitespaces))
        let mode = self.mode
        let serial = port?.serialNumber
        run(session: session, mints: mode == .setup) { p, s, progress, onSession in
            switch mode {
            case .setup:
                return try await p.provision(s, identity: identity, serialNumber: serial,
                                             request: request, progress: progress, onSession: onSession)
            case .changeWiFi:
                let next = try await p.changeWiFi(s, ssid: request.ssid, password: request.password,
                                                  serialNumber: serial, progress: progress, onSession: onSession)
                return (next, nil)
            }
        } finished: { device in
            if let device { await finished(device, old) }
        }
    }

    /// After "Ember rejected the knob's token": mint a new one.
    public func remint(finished: @escaping @MainActor (KnobDevice, KnobDevice?) async -> Void) {
        guard let session, let identity else { return }
        let old = replaces
        let request = KnobSetupRequest(ssid: ssid, password: password, emberURL: emberURL,
                                       name: name.trimmingCharacters(in: .whitespaces))
        let serial = port?.serialNumber
        run(session: session, mints: true) { p, s, progress, onSession in
            try await p.remint(s, identity: identity, serialNumber: serial,
                               request: request, progress: progress, onSession: onSession)
        } finished: { device in
            if let device { await finished(device, old) }
        }
    }

    /// The failed setup minted a new token after the knob already had one:
    /// the old token no longer works, so the knob needs this setup to finish.
    public private(set) var oldTokenRevoked = false

    /// Back to the form after a failure.
    public func edit() {
        guard case .failed = stage, session != nil else { connect(); return }
        Task {
            if await session?.isClosed ?? true { connect() } else { stage = .ready }
        }
    }

    private func run(session: KnobSession, mints: Bool,
                     _ body: @escaping @MainActor (KnobProvisioner, KnobSession,
                                                   @escaping @Sendable (KnobSetupPhase) -> Void,
                                                   @escaping @Sendable (KnobSession) -> Void) async throws
                        -> (KnobSession, KnobDevice?),
                     finished: @escaping @MainActor (KnobDevice?) async -> Void) {
        work?.cancel()
        stage = .sending(.saving)
        oldTokenRevoked = false
        let hadToken = identity?.emberConfigured ?? false
        let p = provisioner
        let latest = LatestSession()
        let progress: @Sendable (KnobSetupPhase) -> Void = { [weak self] phase in
            Task { @MainActor in
                guard let self, case .sending = self.stage else { return }
                self.stage = .sending(phase)
            }
        }
        work = Task {
            do {
                let (next, device) = try await body(p, session, progress, { latest.set($0) })
                try Task.checkCancellation()
                adopt(next)
                self.device = device
                if var id = identity {
                    id.wifiConfigured = true
                    if device != nil { id.emberConfigured = true; id.deviceID = device?.id }
                    identity = id
                }
                await finished(device)
                stage = .done
            } catch is CancellationError {
                // Closed mid-setup: nothing may keep the port open.
                await latest.value?.close()
            } catch {
                // The knob may have rebooted onto a new session: keep it, so
                // Re-mint and Back talk to the live port.
                if let s = latest.value {
                    if Task.isCancelled { await s.close() } else { adopt(s) }
                }
                let e = error as? KnobSetupError ?? .disconnected
                if mints, hadToken {
                    switch e {
                    case .mint, .invalid, .noHardwareID: break
                    default: oldTokenRevoked = true
                    }
                }
                stage = .failed(e)
            }
        }
    }

    private func adopt(_ next: KnobSession) {
        guard next !== session else { return }
        let old = session
        session = next
        Task { await old?.close() }
    }

    /// Closes the port; call when the sheet goes away.
    public func close() {
        work?.cancel()
        work = nil
        let s = session
        session = nil
        Task { await s?.close() }
    }
}

extension KnobSetupModel {
    /// WPA/WPA2: 8–63 characters, or 64 hex digits. Open networks take none.
    public nonisolated static func isValidWPAPassword(_ p: String) -> Bool {
        let n = p.utf8.count
        if (8...63).contains(n) { return true }
        return n == 64 && p.allSatisfy(\.isHexDigit)
    }
}

/// The newest session the provisioner opened (after a reboot).
private final class LatestSession: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: KnobSession?
    var value: KnobSession? { lock.withLock { _value } }
    func set(_ s: KnobSession) { lock.withLock { _value = s } }
}
