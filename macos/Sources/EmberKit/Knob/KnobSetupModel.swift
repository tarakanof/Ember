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

    public var canSend: Bool {
        guard stage == .ready, !ssid.isEmpty else { return false }
        let secured = networks.first { $0.ssid == ssid }?.secured ?? true
        if secured && password.isEmpty { return false }
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

    /// Runs the setup; `finished` gets the registered device.
    public func send(finished: @escaping @MainActor (KnobDevice) async -> Void) {
        guard canSend, let session, let identity else { return }
        let request = KnobSetupRequest(ssid: ssid, password: password, emberURL: emberURL,
                                       name: name.trimmingCharacters(in: .whitespaces))
        run(session: session) { p, s, progress in
            switch self.mode {
            case .setup:
                return try await p.provision(s, identity: identity, serialNumber: self.port?.serialNumber,
                                             request: request, progress: progress)
            case .changeWiFi:
                let next = try await p.changeWiFi(s, ssid: request.ssid, password: request.password,
                                                  serialNumber: self.port?.serialNumber, progress: progress)
                return (next, nil)
            }
        } finished: { device in
            if let device { await finished(device) }
        }
    }

    /// After "Ember rejected the knob's token": mint a new one.
    public func remint(finished: @escaping @MainActor (KnobDevice) async -> Void) {
        guard let session, let identity else { return }
        let request = KnobSetupRequest(ssid: ssid, password: password, emberURL: emberURL,
                                       name: name.trimmingCharacters(in: .whitespaces))
        run(session: session) { p, s, progress in
            try await p.remint(s, identity: identity, serialNumber: self.port?.serialNumber,
                               request: request, progress: progress)
        } finished: { device in
            if let device { await finished(device) }
        }
    }

    /// Back to the form after a failure.
    public func edit() {
        guard case .failed = stage, session != nil else { connect(); return }
        Task {
            if await session?.isClosed ?? true { connect() } else { stage = .ready }
        }
    }

    private func run(session: KnobSession,
                     _ body: @escaping @MainActor (KnobProvisioner, KnobSession,
                                                   @escaping @Sendable (KnobSetupPhase) -> Void) async throws
                        -> (KnobSession, KnobDevice?),
                     finished: @escaping @MainActor (KnobDevice?) async -> Void) {
        work?.cancel()
        stage = .sending(.saving)
        let p = provisioner
        work = Task {
            let progress: @Sendable (KnobSetupPhase) -> Void = { phase in
                Task { @MainActor [weak self] in
                    guard let self, case .sending = self.stage else { return }
                    self.stage = .sending(phase)
                }
            }
            do {
                let (next, device) = try await body(p, session, progress)
                self.session = next
                self.device = device
                if var id = identity {
                    id.wifiConfigured = true
                    if device != nil { id.emberConfigured = true; id.deviceID = device?.id }
                    identity = id
                }
                await finished(device)
                stage = .done
            } catch let e as KnobSetupError {
                stage = .failed(e)
            } catch {
                stage = .failed(.disconnected)
            }
        }
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
