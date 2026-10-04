import Testing
import Foundation
@testable import EmberKit

/// A scripted knob: answers host frames and lines with device bytes, which
/// go back through the real demuxer.
final class FakeKnob: KnobLink, @unchecked Sendable {
    enum Out { case bytes([UInt8]), disconnect }

    let events: AsyncStream<KnobEvent>
    private let continuation: AsyncStream<KnobEvent>.Continuation
    private let lock = NSLock()
    private var hostSide = KnobStreamDemuxer()
    private var deviceSide = KnobStreamDemuxer()
    private var closed = false
    private(set) var received: [String] = []
    var onImprov: (ImprovCodec.Message) -> [Out] = { _ in [] }
    var onCinder: ([String: Any]) -> [Out] = { _ in [] }

    init() {
        (events, continuation) = AsyncStream.makeStream(of: KnobEvent.self)
    }

    func send(_ bytes: [UInt8]) async throws {
        let (outs, isClosed): ([Out], Bool) = lock.withLock {
            guard !closed else { return ([], true) }
            var outs: [Out] = []
            for e in hostSide.feed(bytes) {
                switch e {
                case .improv(let m):
                    received.append("improv \(m)")
                    outs += onImprov(m)
                case .log(let line) where line.hasPrefix(CinderLineCodec.prefix):
                    let json = Data(line.dropFirst(CinderLineCodec.prefix.count).utf8)
                    let obj = (try? JSONSerialization.jsonObject(with: json) as? [String: Any]) ?? [:]
                    received.append("cinder \(obj["op"] ?? "?")")
                    outs += onCinder(obj)
                default: break
                }
            }
            return (outs, false)
        }
        if isClosed { throw KnobLinkError.closed }
        for o in outs { emit(o) }
    }

    func emit(_ o: Out) {
        switch o {
        case .bytes(let b):
            let evs = lock.withLock { deviceSide.feed(b) }
            for e in evs { continuation.yield(e) }
        case .disconnect:
            close()
        }
    }

    func close() {
        lock.withLock { closed = true }
        continuation.finish()
    }

    // Common answers.
    static let info = try! ImprovCodec.result(.deviceInfo, ["cinder", "0.5.0", "ESP32-S3", "Knob 61FC8C"])

    static func reply(_ obj: [String: Any]) -> Out {
        .bytes(Array("CINDER1 ".utf8) + (try! JSONSerialization.data(withJSONObject: obj, options: [.sortedKeys])) + [0x0A])
    }
}

final class FakeOpener: KnobLinkOpener, @unchecked Sendable {
    private let lock = NSLock()
    private var first: FakeKnob?
    private var later: [FakeKnob]
    private(set) var reopenedWith: [String] = []

    init(_ first: FakeKnob, later: [FakeKnob] = []) {
        self.first = first
        self.later = later
    }

    func open(path: String) async throws -> any KnobLink {
        guard let k = lock.withLock({ () -> FakeKnob? in defer { first = nil }; return first }) else {
            throw KnobLinkError.openFailed("busy")
        }
        return k
    }

    func reopen(serialNumber: String, timeout: Duration) async throws -> any KnobLink {
        try lock.withLock {
            reopenedWith.append(serialNumber)
            guard !later.isEmpty else { throw KnobLinkError.notFound }
            return later.removeFirst()
        }
    }
}

private final class Calls: @unchecked Sendable {
    private let lock = NSLock()
    private var _mints: [(String, String)] = []
    var mints: [(String, String)] { lock.withLock { _mints } }
    func mint(_ hw: String, _ name: String) { lock.withLock { _mints.append((hw, name)) } }
}

private final class Phases: @unchecked Sendable {
    private let lock = NSLock()
    private var _all: [KnobSetupPhase] = []
    var all: [KnobSetupPhase] { lock.withLock { _all } }
    func add(_ p: KnobSetupPhase) { lock.withLock { _all.append(p) } }
}

private var fast: KnobProvisioner.Timeouts {
    var t = KnobProvisioner.Timeouts()
    t.info = .milliseconds(200); t.reply = .milliseconds(300); t.scan = .milliseconds(300)
    t.join = .milliseconds(500); t.reconnect = .milliseconds(200); t.ember = .milliseconds(400)
    t.poll = .milliseconds(20)
    return t
}

private let minted = MintedKnob(device: KnobDevice(id: "knob-61fc8c", hwID: "3cdc7561fc8c", name: "Desk knob",
                                                   createdAt: Date(timeIntervalSince1970: 0)),
                                token: "ekd_" + String(repeating: "A", count: 43))

private func provisioner(_ opener: FakeOpener, calls: Calls = Calls(),
                         mintError: Error? = nil, checkedIn: Bool = false) -> KnobProvisioner {
    KnobProvisioner(opener: opener, mint: { hw, name in
        calls.mint(hw, name)
        if let mintError { throw mintError }
        return minted
    }, checkedIn: { _, _ in checkedIn }, timeouts: fast)
}

/// A knob before setup: answers device info, `info`, scan and `set_ember`.
private func freshKnob(wifi: @escaping (FakeKnob) -> [FakeKnob.Out]) -> FakeKnob {
    let k = FakeKnob()
    k.onImprov = { [unowned k] m in
        switch m {
        case .rpc(ImprovCodec.Command.deviceInfo.rawValue, _):
            return [.bytes(Array("I (812) cinder: boot\n".utf8)), .bytes(FakeKnob.info)]
        case .rpc(ImprovCodec.Command.scanNetworks.rawValue, _):
            return [.bytes(try! ImprovCodec.result(.scanNetworks, ["home", "-58", "YES"])),
                    .bytes(try! ImprovCodec.result(.scanNetworks, ["cafe", "-80", "NO"])),
                    .bytes(try! ImprovCodec.result(.scanNetworks, ["home", "-50", "YES"])),
                    .bytes(try! ImprovCodec.result(.scanNetworks, []))]
        case .rpc(ImprovCodec.Command.wifiSettings.rawValue, _):
            return wifi(k)
        default:
            return []
        }
    }
    k.onCinder = { obj in
        let id = obj["id"] as? Int ?? 0
        switch obj["op"] as? String {
        case "info":
            return [FakeKnob.reply(["id": id, "ok": true, "fw": "0.5.0", "hw_id": "3cdc7561fc8c",
                                    "wifi": ["configured": false], "ember": ["configured": false]])]
        case "set_ember":
            return [FakeKnob.reply(["id": id, "ok": true])]
        default:
            return []
        }
    }
    return k
}

/// The knob after its reboot: joined, answers `status` with `ember`.
private func rebootedKnob(ember: String) -> FakeKnob {
    let k = FakeKnob()
    k.onImprov = { m in
        if case .rpc(ImprovCodec.Command.currentState.rawValue, _) = m {
            return [.bytes(Array("I (40) wifi: connected\n".utf8)),
                    .bytes(CinderLineCodec.line(CinderLineCodec.Event(ev: "boot", fw: "0.5.0", provisioned: true))),
                    .bytes(ImprovCodec.state(.provisioned))]
        }
        return []
    }
    k.onCinder = { obj in
        guard obj["op"] as? String == "status" else { return [] }
        return [FakeKnob.reply(["id": obj["id"] as? Int ?? 0, "ok": true,
                                "wifi": ["state": "connected", "ip": "192.168.0.39"], "ember": ["state": ember]])]
    }
    return k
}

private let request = KnobSetupRequest(ssid: "home", password: "hunter22", emberURL: "http://192.168.0.2:3627", name: "Desk knob")

@Test func connectIdentifiesAndScans() async throws {
    let knob = freshKnob { _ in [] }
    let p = provisioner(FakeOpener(knob))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    #expect(id.info.isCinder)
    #expect(id.hwID == "3cdc7561fc8c")
    #expect(id.shortID == "61FC8C")
    #expect(!id.isProvisioned)
    let nets = try await p.scan(session)
    #expect(nets.map(\.ssid) == ["home", "cafe"])
    #expect(nets.first?.rssi == -50)
}

@Test func connectWithoutImprovIsNotCinder() async throws {
    let silent = FakeKnob()
    let p = provisioner(FakeOpener(silent))
    await #expect(throws: KnobSetupError.notCinder) { _ = try await p.connect(path: "/dev/cu.fake", usbHwID: nil) }
}

@Test func happyPathFollowsRebootAndReconnectsBySerial() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    let opener = FakeOpener(knob, later: [rebootedKnob(ember: "ok")])
    let calls = Calls()
    let phases = Phases()
    let p = provisioner(opener, calls: calls)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    let (_, device) = try await p.provision(session, identity: id, serialNumber: "3C:DC:75:61:FC:8C",
                                            request: request, progress: { phases.add($0) })
    #expect(device.id == "knob-61fc8c")
    #expect(calls.mints.map(\.0) == ["3cdc7561fc8c"])
    #expect(calls.mints.map(\.1) == ["Desk knob"])
    #expect(opener.reopenedWith == ["3C:DC:75:61:FC:8C"])
    #expect(phases.all == [.saving, .restarting, .joining(ssid: "home"), .reachingEmber, .done])
    #expect(knob.received.contains("cinder set_ember"))
    // set_ember goes before the Wi-Fi RPC, which reboots the knob.
    let order = knob.received.filter { $0 == "cinder set_ember" || $0.hasPrefix("improv rpc(command: 1") }
    #expect(order.first == "cinder set_ember")
}

@Test func wrongPasswordMapsToWiFiError() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .bytes(ImprovCodec.error(.unableToConnect))] }
    let p = provisioner(FakeOpener(knob))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    await #expect(throws: KnobSetupError.wifi(ssid: "home")) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
}

@Test func mintRejectedStopsBeforeTheKnobChanges() async throws {
    let knob = freshKnob { _ in [] }
    let p = provisioner(FakeOpener(knob), mintError: APIError.http(status: 401, body: ""))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    await #expect(throws: KnobSetupError.mint(.unauthorized)) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
    #expect(!knob.received.contains("cinder set_ember"))
}

@Test func emberUnauthorizedIsReported() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    let p = provisioner(FakeOpener(knob, later: [rebootedKnob(ember: "unauthorized")]))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    await #expect(throws: KnobSetupError.emberUnauthorized) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
}

@Test func emberUnreachableCarriesTheKnobIP() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    let p = provisioner(FakeOpener(knob, later: [rebootedKnob(ember: "unreachable")]))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    await #expect(throws: KnobSetupError.emberUnreachable(ip: "192.168.0.39", url: "http://192.168.0.2:3627")) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
}

@Test func serverCheckinCountsAsReachingEmber() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioned))] }
    let p = provisioner(FakeOpener(knob), checkedIn: true)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    let (_, device) = try await p.provision(session, identity: id, serialNumber: nil, request: request, progress: { _ in })
    #expect(device.name == "Desk knob")
}

@Test func knobGoneAfterRebootIsDisconnected() async throws {
    let knob = freshKnob { _ in [.disconnect] }
    let p = provisioner(FakeOpener(knob))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    await #expect(throws: KnobSetupError.disconnected) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
}

@Test func setEmberRejectionIsShown() async throws {
    let knob = freshKnob { _ in [] }
    knob.onCinder = { obj in
        let id = obj["id"] as? Int ?? 0
        if obj["op"] as? String == "set_ember" { return [FakeKnob.reply(["id": id, "ok": false, "error": "bad_url"])] }
        return [FakeKnob.reply(["id": id, "ok": true, "hw_id": "3cdc7561fc8c"])]
    }
    let p = provisioner(FakeOpener(knob))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    await #expect(throws: KnobSetupError.rejected("bad_url")) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
}

@Test func factoryResetSendsResetFactory() async throws {
    let knob = freshKnob { _ in [] }
    knob.onCinder = { obj in [FakeKnob.reply(["id": obj["id"] as? Int ?? 0, "ok": true])] }
    let p = provisioner(FakeOpener(knob))
    let (session, _) = try await p.connect(path: "/dev/cu.fake", usbHwID: "3cdc7561fc8c")
    try await p.factoryReset(session)
    #expect(knob.received.contains("cinder reset"))
}
