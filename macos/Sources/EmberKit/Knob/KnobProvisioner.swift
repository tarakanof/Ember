import Foundation

public enum KnobSetupPhase: Equatable, Sendable {
    case saving
    case restarting
    case joining(ssid: String)
    case reachingEmber
    case done
}

public enum KnobSetupError: Error, Equatable, Sendable {
    case notCinder
    case disconnected
    case noHardwareID
    case mint(FeedError)
    case rejected(String)
    case wifi(ssid: String)
    case wifiRejected
    case improv(UInt8)
    case emberUnreachable(ip: String?, url: String)
    case emberUnauthorized
    case timedOut(KnobSetupPhase)
    case invalid(String)
}

public struct KnobIdentity: Equatable, Sendable {
    public var info: ImprovDeviceInfo
    public var hwID: String?
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

public struct KnobProvisioner: Sendable {
    public struct Timeouts: Sendable {
        public var info: Duration = .seconds(2)
        public var reply: Duration = .seconds(3)
        public var scan: Duration = .seconds(15)
        public var join: Duration = .seconds(45)
        public var reconnect: Duration = .seconds(30)
        public var ember: Duration = .seconds(30)
        public var poll: Duration = .seconds(3)
        public init() {}
    }

    let opener: any KnobLinkOpener
    let mint: @Sendable (_ hwID: String, _ name: String) async throws -> MintedKnob
    let checkedIn: @Sendable (_ id: String, _ baseline: Date?) async -> Bool
    let forget: @Sendable (_ id: String) async -> Void
    let timeouts: Timeouts

    public init(opener: any KnobLinkOpener,
                mint: @escaping @Sendable (String, String) async throws -> MintedKnob,
                checkedIn: @escaping @Sendable (String, Date?) async -> Bool,
                forget: @escaping @Sendable (String) async -> Void = { _ in },
                timeouts: Timeouts = Timeouts()) {
        self.opener = opener; self.mint = mint; self.checkedIn = checkedIn
        self.forget = forget; self.timeouts = timeouts
    }

    public init(opener: any KnobLinkOpener, service: KnobService, timeouts: Timeouts = Timeouts()) {
        self.init(opener: opener,
                  mint: { try await service.mint(hwID: $0, name: $1) },
                  checkedIn: { id, baseline in
                      let d = try? await service.devices().first { $0.id == id }
                      return Self.checkedIn(d?.lastCheckin?.seenAt, after: baseline)
                  },
                  forget: { try? await service.forget(id: $0) },
                  timeouts: timeouts)
    }

    static func checkedIn(_ seen: Date?, after baseline: Date?) -> Bool {
        guard let seen else { return false }
        guard let baseline else { return true }
        return seen > baseline
    }

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

    private enum Hello: Sendable {
        case info(ImprovDeviceInfo)
        case boot(fw: String?)
    }

    func identify(_ session: KnobSession, usbHwID: String?, attempts: Int = 3) async throws -> KnobIdentity {
        var info: ImprovDeviceInfo?
        var bootFW: String?
        for _ in 0..<attempts where info == nil {
            try Task.checkCancellation()
            do {
                try await session.send(ImprovCodec.rpc(.deviceInfo))
                let hello = try await session.expect(timeout: timeouts.info) { e -> Hello? in
                    switch e {
                    case .improv(.result(ImprovCodec.Command.deviceInfo.rawValue, let s)):
                        return ImprovDeviceInfo(strings: s).map(Hello.info)
                    case .cinder(.event(let ev)) where ev.ev == "boot":
                        return .boot(fw: ev.fw)
                    default:
                        return nil
                    }
                }
                switch hello {
                case .info(let i): info = i
                case .boot(let fw): bootFW = fw ?? ""
                }
            } catch is KnobTimeout {
                continue
            } catch is CancellationError {
                throw CancellationError()
            } catch {
                throw KnobSetupError.disconnected
            }
        }
        if info == nil, let fw = bootFW {
            let short = usbHwID.map { String($0.suffix(6)).uppercased() } ?? ""
            info = ImprovDeviceInfo(firmware: "cinder", version: fw, chip: "ESP32-S3", name: "Knob \(short)")
        }
        guard let info, info.isCinder else { throw KnobSetupError.notCinder }
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

    public func provision(_ session: KnobSession, identity: KnobIdentity, serialNumber: String?,
                          request: KnobSetupRequest,
                          progress: @escaping @Sendable (KnobSetupPhase) -> Void,
                          onSession: @escaping @Sendable (KnobSession) -> Void = { _ in })
        async throws -> (KnobSession, KnobDevice) {
        progress(.saving)
        let request = try Self.validated(request)
        guard let hwID = identity.hwID else { throw KnobSetupError.noHardwareID }
        let minted = try await mintToken(hwID: hwID, name: request.name)
        let s = try await cleaningUp(minted) {
            try await setEmber(session, minted: minted, request: request)
            let joined = try await join(session, ssid: request.ssid, password: request.password,
                                        serialNumber: serialNumber, progress: progress, onSession: onSession)
            progress(.reachingEmber)
            return try await waitForEmber(joined, deviceID: minted.device.id, baseline: minted.device.lastCheckin?.seenAt,
                                          url: request.emberURL, serialNumber: serialNumber, onSession: onSession)
        }
        progress(.done)
        return (s, minted.device)
    }

    private func cleaningUp(_ minted: MintedKnob, _ body: () async throws -> KnobSession) async throws -> KnobSession {
        do {
            return try await body()
        } catch {
            if minted.device.lastCheckin == nil {
                // Unstructured: a cancelled setup must still send the DELETE.
                let forget = self.forget, id = minted.device.id
                await Task.detached { await forget(id) }.value
            }
            throw error
        }
    }

    public func remint(_ session: KnobSession, identity: KnobIdentity, serialNumber: String?,
                       request: KnobSetupRequest,
                       progress: @escaping @Sendable (KnobSetupPhase) -> Void,
                       onSession: @escaping @Sendable (KnobSession) -> Void = { _ in })
        async throws -> (KnobSession, KnobDevice) {
        progress(.saving)
        let request = try Self.validated(request)
        guard let hwID = identity.hwID else { throw KnobSetupError.noHardwareID }
        let minted = try await mintToken(hwID: hwID, name: request.name)
        let s = try await cleaningUp(minted) {
            await session.drain()
            try await setEmber(session, minted: minted, request: request)
            await session.drain()
            progress(.reachingEmber)
            return try await waitForEmber(session, deviceID: minted.device.id, baseline: minted.device.lastCheckin?.seenAt,
                                          url: request.emberURL, serialNumber: serialNumber, onSession: onSession)
        }
        progress(.done)
        return (s, minted.device)
    }

    public func changeWiFi(_ session: KnobSession, ssid: String, password: String, serialNumber: String?,
                           progress: @escaping @Sendable (KnobSetupPhase) -> Void,
                           onSession: @escaping @Sendable (KnobSession) -> Void = { _ in }) async throws -> KnobSession {
        progress(.saving)
        let s = try await join(session, ssid: ssid, password: password, serialNumber: serialNumber,
                               progress: progress, onSession: onSession)
        progress(.done)
        return s
    }

    public func factoryReset(_ session: KnobSession) async throws {
        let r = try await session.call(.reset(.factory), timeout: timeouts.reply)
        guard r.ok else { throw KnobSetupError.rejected(r.error ?? "unknown") }
    }

    private func mintToken(hwID: String, name: String) async throws -> MintedKnob {
        do { return try await mint(hwID, name) } catch { throw KnobSetupError.mint(FeedError(error)) }
    }

    static func validated(_ request: KnobSetupRequest) throws -> KnobSetupRequest {
        guard let url = CinderLineCodec.normalizedEmberURL(request.emberURL) else {
            throw KnobSetupError.invalid(request.emberURL)
        }
        var r = request
        r.emberURL = url
        r.name = CinderLineCodec.cappedName(request.name.trimmingCharacters(in: .whitespacesAndNewlines))
        return r
    }

    static func knobName(_ minted: MintedKnob, fallback: String) -> String {
        let n = CinderLineCodec.cappedName(minted.device.name.trimmingCharacters(in: .whitespacesAndNewlines))
        if !n.isEmpty { return n }
        return fallback.isEmpty ? "Knob \(minted.device.shortID)" : fallback
    }

    private func setEmber(_ session: KnobSession, minted: MintedKnob, request: KnobSetupRequest) async throws {
        let reply: CinderLineCodec.Reply
        do {
            reply = try await session.call(.setEmber(url: request.emberURL, deviceID: minted.device.id,
                                                      token: minted.token,
                                                      name: Self.knobName(minted, fallback: request.name)),
                                           timeout: timeouts.reply)
        } catch let e as CinderLineCodec.EncodeError {
            throw KnobSetupError.rejected(e == .badURL ? "bad_url" : "too_long")
        } catch is KnobTimeout {
            throw KnobSetupError.timedOut(.saving)
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            throw KnobSetupError.disconnected
        }
        guard reply.ok else { throw KnobSetupError.rejected(reply.error ?? "unknown") }
    }

    private enum JoinEvent: Sendable {
        case state(ImprovCodec.State), error(ImprovCodec.ErrorCode), boot, wifiInvalid
    }

    func join(_ session: KnobSession, ssid: String, password: String, serialNumber: String?,
              progress: @escaping @Sendable (KnobSetupPhase) -> Void,
              onSession: @escaping @Sendable (KnobSession) -> Void) async throws -> KnobSession {
        await session.drain()
        var s = session
        var phase = KnobSetupPhase.saving
        func set(_ p: KnobSetupPhase) { if p != phase { phase = p; progress(p) } }
        let wifi = try ImprovCodec.wifiSettings(ssid: ssid, password: password)
        var wifiSends = 0
        var restartedSinceWiFi = false
        func sendWiFi() async throws {
            try Task.checkCancellation()
            wifiSends += 1
            restartedSinceWiFi = false
            do {
                try await s.send(wifi)
            } catch {
                set(.restarting)
                s = try await reconnect(serialNumber, onSession: onSession)
                restartedSinceWiFi = true
                try? await s.send(ImprovCodec.rpc(.currentState))
            }
        }
        try await sendWiFi()
        while true {
            try Task.checkCancellation()
            let ev: JoinEvent
            do {
                ev = try await s.expect(timeout: timeouts.join) { e -> JoinEvent? in
                    switch e {
                    case .improv(.state(let st)): return .state(st)
                    case .improv(.error(let code)) where code != .none: return .error(code)
                    case .cinder(.event(let ev)) where ev.ev == "boot": return .boot
                    case .cinder(.event(let ev)) where ev.ev == "wifi" && ev.state == "invalid": return .wifiInvalid
                    default: return nil
                    }
                }
            } catch is KnobTimeout {
                throw KnobSetupError.timedOut(phase)
            } catch is CancellationError {
                throw CancellationError()
            } catch {
                set(.restarting)
                s = try await reconnect(serialNumber, onSession: onSession)
                restartedSinceWiFi = true
                set(.joining(ssid: ssid))
                try? await s.send(ImprovCodec.rpc(.currentState))
                continue
            }
            switch ev {
            case .state(.provisioned):
                return s
            case .state(.provisioning):
                set(.restarting)
            case .state(.ready) where restartedSinceWiFi && wifiSends < 3:
                try await sendWiFi()
            case .state:
                set(.joining(ssid: ssid))
            case .boot:
                set(.joining(ssid: ssid))
            case .error(.unableToConnect):
                throw KnobSetupError.wifi(ssid: ssid)
            case .error(.invalidRPC), .wifiInvalid:
                throw KnobSetupError.wifiRejected
            case .error(let code):
                throw KnobSetupError.improv(code.rawValue)
            }
        }
    }

    private func reconnect(_ serialNumber: String?,
                           onSession: @Sendable (KnobSession) -> Void) async throws -> KnobSession {
        try Task.checkCancellation()
        guard let serialNumber else { throw KnobSetupError.disconnected }
        let link: any KnobLink
        do {
            link = try await opener.reopen(serialNumber: serialNumber, timeout: timeouts.reconnect)
        } catch {
            throw KnobSetupError.disconnected
        }
        let s = KnobSession(link: link)
        await s.start()
        onSession(s)
        return s
    }

    private enum EmberEvent: Sendable {
        case state(String, ip: String?)
    }

    func waitForEmber(_ session: KnobSession, deviceID: String, baseline: Date?, url: String,
                      serialNumber: String?,
                      onSession: @escaping @Sendable (KnobSession) -> Void) async throws -> KnobSession {
        let clock = ContinuousClock()
        let deadline = clock.now.advanced(by: timeouts.ember)
        var s = session
        var lastState: String?
        var ip: String?
        var reconnected = false
        while clock.now < deadline {
            try Task.checkCancellation()
            if await checkedIn(deviceID, baseline) { return s }
            var statusID: Int?
            do {
                statusID = try await s.send(.status)
            } catch {
                guard !reconnected else { throw KnobSetupError.disconnected }
                reconnected = true
                s = try await reconnect(serialNumber, onSession: onSession)
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
                case "unreachable": lastState = st
                default: break
                }
                try? await Task.sleep(for: timeouts.poll)
            }
        }
        if lastState == "unreachable" { throw KnobSetupError.emberUnreachable(ip: ip, url: url) }
        throw KnobSetupError.timedOut(.reachingEmber)
    }
}
