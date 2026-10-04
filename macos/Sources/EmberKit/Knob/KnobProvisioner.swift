import Foundation

/// Where a setup is; both screens show it.
public enum KnobSetupPhase: Equatable, Sendable {
    case saving
    case restarting
    case joining(ssid: String)
    case reachingEmber
    case done
}

public enum KnobSetupError: Error, Equatable, Sendable {
    /// No Improv answer: an ESP32 board that isn't running cinder.
    case notCinder
    /// The knob went away and didn't come back.
    case disconnected
    /// The knob reported no hardware ID and its USB serial number isn't one.
    case noHardwareID
    /// The server didn't mint a token.
    case mint(FeedError)
    /// The knob refused `set_ember` (`bad_url`, `too_long`, …).
    case rejected(String)
    /// Improv `unable to connect`.
    case wifi(ssid: String)
    /// Any other Improv error code.
    case improv(UInt8)
    /// On Wi-Fi at `ip` but can't reach `url`.
    case emberUnreachable(ip: String?, url: String)
    /// Ember rejected the knob's token.
    case emberUnauthorized
    case timedOut(KnobSetupPhase)
    /// A host-side problem (the URL failed validation).
    case invalid(String)
}

/// What the knob says about itself on connect.
public struct KnobIdentity: Equatable, Sendable {
    public var info: ImprovDeviceInfo
    /// `hw_id` from `CINDER1 info`, else from the USB serial number.
    public var hwID: String?
    /// The registry id the knob was given, nil when unprovisioned.
    public var deviceID: String?
    public var wifiConfigured: Bool
    public var emberConfigured: Bool

    public init(info: ImprovDeviceInfo, hwID: String?, deviceID: String? = nil,
                wifiConfigured: Bool = false, emberConfigured: Bool = false) {
        self.info = info; self.hwID = hwID; self.deviceID = deviceID
        self.wifiConfigured = wifiConfigured; self.emberConfigured = emberConfigured
    }

    public var shortID: String? { hwID.map { String($0.suffix(6)).uppercased() } }
    public var isProvisioned: Bool { wifiConfigured && emberConfigured }
}

public struct KnobSetupRequest: Equatable, Sendable {
    public var ssid: String
    public var password: String
    public var emberURL: String
    public var name: String

    public init(ssid: String, password: String, emberURL: String, name: String) {
        self.ssid = ssid; self.password = password; self.emberURL = emberURL; self.name = name
    }
}

/// The setup protocol: Improv for Wi-Fi, `CINDER1` for Ember, the server
/// for the token. No UI; the link and the server are injected.
public struct KnobProvisioner: Sendable {
    public struct Timeouts: Sendable {
        /// Improv device info: no answer = not cinder.
        public var info: Duration = .seconds(2)
        public var reply: Duration = .seconds(3)
        public var scan: Duration = .seconds(15)
        /// Wi-Fi RPC to the knob joining (reboot included).
        public var join: Duration = .seconds(45)
        public var reconnect: Duration = .seconds(30)
        /// Joined to the first good checkin.
        public var ember: Duration = .seconds(30)
        /// Between `status` polls while waiting for Ember.
        public var poll: Duration = .seconds(3)
        public init() {}
    }

    let opener: any KnobLinkOpener
    let mint: @Sendable (_ hwID: String, _ name: String) async throws -> MintedKnob
    /// Whether the server has seen a checkin from `id` since `since`.
    let checkedIn: @Sendable (_ id: String, _ since: Date) async -> Bool
    let timeouts: Timeouts
    let now: @Sendable () -> Date

    public init(opener: any KnobLinkOpener,
                mint: @escaping @Sendable (String, String) async throws -> MintedKnob,
                checkedIn: @escaping @Sendable (String, Date) async -> Bool,
                timeouts: Timeouts = Timeouts(),
                now: @escaping @Sendable () -> Date = { Date() }) {
        self.opener = opener; self.mint = mint; self.checkedIn = checkedIn
        self.timeouts = timeouts; self.now = now
    }

    /// A provisioner over `/v1/devices`.
    public init(opener: any KnobLinkOpener, service: KnobService, timeouts: Timeouts = Timeouts()) {
        self.init(opener: opener,
                  mint: { try await service.mint(hwID: $0, name: $1) },
                  checkedIn: { id, since in
                      let d = try? await service.devices().first { $0.id == id }
                      return (d?.lastCheckin?.seenAt).map { $0 >= since } ?? false
                  },
                  timeouts: timeouts)
    }

    // MARK: Connect

    /// Opens the port and asks the knob who it is.
    public func connect(path: String, usbHwID: String?) async throws -> (KnobSession, KnobIdentity) {
        let link = try await opener.open(path: path)
        let session = KnobSession(link: link)
        await session.start()
        do {
            return (session, try await identify(session, usbHwID: usbHwID))
        } catch {
            await session.close()
            throw error
        }
    }

    func identify(_ session: KnobSession, usbHwID: String?) async throws -> KnobIdentity {
        try await session.send(ImprovCodec.rpc(.deviceInfo))
        let info: ImprovDeviceInfo
        do {
            info = try await session.expect(timeout: timeouts.info) { e in
                if case .improv(.result(ImprovCodec.Command.deviceInfo.rawValue, let s)) = e {
                    return ImprovDeviceInfo(strings: s)
                }
                return nil
            }
        } catch is KnobTimeout {
            throw KnobSetupError.notCinder
        } catch {
            throw KnobSetupError.disconnected
        }
        guard info.isCinder else { throw KnobSetupError.notCinder }
        var id = KnobIdentity(info: info, hwID: usbHwID)
        if let r = try? await session.call(.info, timeout: timeouts.reply), r.ok {
            id.hwID = r.hwID.flatMap(Self.normalizeHwID) ?? usbHwID
            id.deviceID = r.deviceID
            id.wifiConfigured = r.wifi?.configured ?? false
            id.emberConfigured = r.ember?.configured ?? false
        }
        return id
    }

    static func normalizeHwID(_ s: String) -> String? {
        let hex = s.lowercased().filter { $0 != ":" && $0 != "-" }
        return hex.count == 12 && hex.allSatisfy(\.isHexDigit) ? hex : nil
    }

    // MARK: Scan

    /// The networks the knob hears, strongest first.
    public func scan(_ session: KnobSession) async throws -> [KnobWiFiNetwork] {
        await session.drain()
        try await session.send(ImprovCodec.rpc(.scanNetworks))
        var found: [KnobWiFiNetwork] = []
        while true {
            let strings = try await session.expect(timeout: timeouts.scan) { e -> [String]? in
                if case .improv(.result(ImprovCodec.Command.scanNetworks.rawValue, let s)) = e { return s }
                return nil
            }
            guard let n = KnobWiFiNetwork(strings: strings) else { break }
            found.append(n)
        }
        return KnobWiFiNetwork.dedupe(found)
    }

    // MARK: Provision

    /// Mints a token, hands the knob its Ember settings and Wi-Fi, follows
    /// the reboot and waits for the first good checkin. Returns the session
    /// (a new one after a reconnect) and the registered device.
    public func provision(_ session: KnobSession, identity: KnobIdentity, serialNumber: String?,
                          request: KnobSetupRequest,
                          progress: @escaping @Sendable (KnobSetupPhase) -> Void) async throws -> (KnobSession, KnobDevice) {
        progress(.saving)
        guard CinderLineCodec.isValidEmberURL(request.emberURL) else { throw KnobSetupError.invalid(request.emberURL) }
        guard let hwID = identity.hwID else { throw KnobSetupError.noHardwareID }
        let started = now()
        let minted = try await mintToken(hwID: hwID, name: request.name)
        try await setEmber(session, minted: minted, request: request)
        var s = try await join(session, ssid: request.ssid, password: request.password,
                               serialNumber: serialNumber, progress: progress)
        progress(.reachingEmber)
        s = try await waitForEmber(s, deviceID: minted.device.id, since: started,
                                   url: request.emberURL, serialNumber: serialNumber)
        progress(.done)
        return (s, minted.device)
    }

    /// After "Ember rejected the knob's token": a new token, no Wi-Fi change.
    public func remint(_ session: KnobSession, identity: KnobIdentity, serialNumber: String?,
                       request: KnobSetupRequest,
                       progress: @escaping @Sendable (KnobSetupPhase) -> Void) async throws -> (KnobSession, KnobDevice) {
        progress(.saving)
        guard let hwID = identity.hwID else { throw KnobSetupError.noHardwareID }
        let started = now()
        let minted = try await mintToken(hwID: hwID, name: request.name)
        try await setEmber(session, minted: minted, request: request)
        progress(.reachingEmber)
        let s = try await waitForEmber(session, deviceID: minted.device.id, since: started,
                                       url: request.emberURL, serialNumber: serialNumber)
        progress(.done)
        return (s, minted.device)
    }

    /// New Wi-Fi only (Advanced › Change Wi-Fi).
    public func changeWiFi(_ session: KnobSession, ssid: String, password: String, serialNumber: String?,
                           progress: @escaping @Sendable (KnobSetupPhase) -> Void) async throws -> KnobSession {
        progress(.saving)
        let s = try await join(session, ssid: ssid, password: password, serialNumber: serialNumber, progress: progress)
        progress(.done)
        return s
    }

    /// Erases the knob's settings and Wi-Fi; it reboots to its setup face.
    public func factoryReset(_ session: KnobSession) async throws {
        let r = try await session.call(.reset(.factory), timeout: timeouts.reply)
        guard r.ok else { throw KnobSetupError.rejected(r.error ?? "unknown") }
    }

    private func mintToken(hwID: String, name: String) async throws -> MintedKnob {
        do { return try await mint(hwID, name) } catch { throw KnobSetupError.mint(FeedError(error)) }
    }

    private func setEmber(_ session: KnobSession, minted: MintedKnob, request: KnobSetupRequest) async throws {
        let reply: CinderLineCodec.Reply
        do {
            reply = try await session.call(.setEmber(url: request.emberURL, deviceID: minted.device.id,
                                                      token: minted.token, name: request.name),
                                           timeout: timeouts.reply)
        } catch let e as CinderLineCodec.EncodeError {
            throw KnobSetupError.rejected(e == .badURL ? "bad_url" : "too_long")
        } catch is KnobTimeout {
            throw KnobSetupError.timedOut(.saving)
        } catch {
            throw KnobSetupError.disconnected
        }
        guard reply.ok else { throw KnobSetupError.rejected(reply.error ?? "unknown") }
    }

    private enum JoinEvent: Sendable {
        case state(ImprovCodec.State), error(ImprovCodec.ErrorCode), boot
    }

    /// Sends the Wi-Fi RPC and follows the knob until it reports provisioned.
    func join(_ session: KnobSession, ssid: String, password: String, serialNumber: String?,
              progress: @escaping @Sendable (KnobSetupPhase) -> Void) async throws -> KnobSession {
        await session.drain()
        do {
            try await session.send(ImprovCodec.wifiSettings(ssid: ssid, password: password))
        } catch {
            throw KnobSetupError.disconnected
        }
        var s = session
        var phase = KnobSetupPhase.saving
        func set(_ p: KnobSetupPhase) { if p != phase { phase = p; progress(p) } }
        while true {
            let ev: JoinEvent
            do {
                ev = try await s.expect(timeout: timeouts.join) { e -> JoinEvent? in
                    switch e {
                    case .improv(.state(let st)): return .state(st)
                    case .improv(.error(let code)) where code != .none: return .error(code)
                    case .cinder(.event(let ev)) where ev.ev == "boot": return .boot
                    default: return nil
                    }
                }
            } catch is KnobTimeout {
                throw KnobSetupError.timedOut(phase)
            } catch {
                set(.restarting)
                s = try await reconnect(serialNumber)
                set(.joining(ssid: ssid))
                try? await s.send(ImprovCodec.rpc(.currentState))
                continue
            }
            switch ev {
            case .state(.provisioned):
                return s
            case .state(.provisioning):
                set(.restarting)
            case .state:
                set(.joining(ssid: ssid))
            case .boot:
                set(.joining(ssid: ssid))
            case .error(.unableToConnect):
                throw KnobSetupError.wifi(ssid: ssid)
            case .error(let code):
                throw KnobSetupError.improv(code.rawValue)
            }
        }
    }

    private func reconnect(_ serialNumber: String?) async throws -> KnobSession {
        guard let serialNumber else { throw KnobSetupError.disconnected }
        let link: any KnobLink
        do {
            link = try await opener.reopen(serialNumber: serialNumber, timeout: timeouts.reconnect)
        } catch {
            throw KnobSetupError.disconnected
        }
        let s = KnobSession(link: link)
        await s.start()
        return s
    }

    private enum EmberEvent: Sendable {
        case state(String, ip: String?)
    }

    /// Waits for `ember: ok` from the knob (event or `status`) or a checkin
    /// on the server.
    func waitForEmber(_ session: KnobSession, deviceID: String, since: Date, url: String,
                      serialNumber: String?) async throws -> KnobSession {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeouts.ember)
        var s = session
        var lastState: String?
        var ip: String?
        var reconnected = false
        while clock.now < deadline {
            if await checkedIn(deviceID, since.addingTimeInterval(-5)) { return s }
            var statusID: Int?
            do {
                statusID = try await s.send(.status)
            } catch {
                guard !reconnected else { throw KnobSetupError.disconnected }
                reconnected = true
                s = try await reconnect(serialNumber)
                continue
            }
            let id = statusID
            let got = try? await s.expect(timeout: timeouts.poll) { e -> EmberEvent? in
                switch e {
                case .cinder(.event(let ev)) where ev.ev == "ember":
                    return ev.state.map { .state($0, ip: nil) }
                case .cinder(.reply(let r)) where r.id == id:
                    return r.ember?.state.map { .state($0, ip: r.wifi?.ip) } ?? .state("", ip: r.wifi?.ip)
                default:
                    return nil
                }
            }
            if case .state(let st, let addr)? = got {
                if let addr { ip = addr }
                switch st {
                case "ok": return s
                case "unauthorized": throw KnobSetupError.emberUnauthorized
                case "": break
                default: lastState = st
                }
                try? await Task.sleep(for: timeouts.poll)
            }
        }
        if lastState == "unreachable" { throw KnobSetupError.emberUnreachable(ip: ip, url: url) }
        throw KnobSetupError.timedOut(.reachingEmber)
    }
}
