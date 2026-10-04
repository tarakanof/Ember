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
    private var _forgets: [String] = []
    var forgets: [String] { lock.withLock { _forgets } }
    func forget(_ id: String) { lock.withLock { _forgets.append(id) } }
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

private func provisioner(_ opener: FakeOpener, calls: Calls = Calls(), minted: MintedKnob = minted,
                         mintError: Error? = nil, checkedIn: Bool = false) -> KnobProvisioner {
    KnobProvisioner(opener: opener, mint: { hw, name in
        calls.mint(hw, name)
        if let mintError { throw mintError }
        return minted
    }, checkedIn: { _, _ in checkedIn }, forget: { calls.forget($0) }, timeouts: fast)
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
/// `okAfterSetEmber`: a new token (re-mint) makes Ember answer ok; the
/// knob also announces the old token's failure unprompted.
private func rebootedKnob(ember: String, okAfterSetEmber: Bool = false) -> FakeKnob {
    let k = FakeKnob()
    let state = StateBox(ember)
    k.onImprov = { m in
        if case .rpc(ImprovCodec.Command.currentState.rawValue, _) = m {
            return [.bytes(Array("I (40) wifi: connected\n".utf8)),
                    .bytes(CinderLineCodec.line(CinderLineCodec.Event(ev: "boot", fw: "0.5.0", provisioned: true))),
                    .bytes(ImprovCodec.state(.provisioned)),
                    .bytes(CinderLineCodec.line(CinderLineCodec.Event(ev: "ember", state: state.value)))]
        }
        return []
    }
    k.onCinder = { obj in
        let id = obj["id"] as? Int ?? 0
        switch obj["op"] as? String {
        case "set_ember":
            if okAfterSetEmber { state.value = "ok" }
            return [FakeKnob.reply(["id": id, "ok": true])]
        case "status":
            return [FakeKnob.reply(["id": id, "ok": true,
                                    "wifi": ["state": "connected", "ip": "192.168.0.39"], "ember": ["state": state.value]])]
        default:
            return []
        }
    }
    return k
}

final class StateBox: @unchecked Sendable {
    private let lock = NSLock()
    private var _v: String
    init(_ v: String) { _v = v }
    var value: String {
        get { lock.withLock { _v } }
        set { lock.withLock { _v = newValue } }
    }
}

final class SessionSink: @unchecked Sendable {
    private let lock = NSLock()
    private var _all: [KnobSession] = []
    var all: [KnobSession] { lock.withLock { _all } }
    func add(_ s: KnobSession) { lock.withLock { _all.append(s) } }
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

// MARK: Review fixes

@Test func remintAfterRebootUsesTheLiveSessionAndIgnoresStaleEvents() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    let calls = Calls()
    let p = provisioner(FakeOpener(knob, later: [rebootedKnob(ember: "unauthorized", okAfterSetEmber: true)]), calls: calls)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    let sink = SessionSink()
    await #expect(throws: KnobSetupError.emberUnauthorized) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request,
                                  progress: { _ in }, onSession: { sink.add($0) })
    }
    let live = try #require(sink.all.last)
    #expect(await !live.isClosed)
    // The rebooted knob already sent "unauthorized" for the old token; the
    // re-mint must wait for a fresh answer.
    let (_, device) = try await p.remint(live, identity: id, serialNumber: "x", request: request, progress: { _ in })
    #expect(device.id == "knob-61fc8c")
    #expect(calls.mints.count == 2)
}

@Test func failedSetupDeletesARecordThatNeverWorked() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.error(.unableToConnect))] }
    let calls = Calls()
    let p = provisioner(FakeOpener(knob), calls: calls)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    await #expect(throws: KnobSetupError.wifi(ssid: "home")) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
    #expect(calls.forgets == ["knob-61fc8c"])
}

@Test func failedResetupKeepsAWorkingRecord() async throws {
    var known = minted.device
    known.lastCheckin = KnobCheckin(seenAt: Date(timeIntervalSince1970: 100), fw: "0.5.0", ip: "192.168.0.39", rssi: -58,
                                    heapInternalFree: 1, heapInternalLargest: 1, uptimeS: 1, appliedVersion: 1)
    let knob = freshKnob { _ in [.bytes(ImprovCodec.error(.unableToConnect))] }
    let calls = Calls()
    let p = provisioner(FakeOpener(knob), calls: calls, minted: MintedKnob(device: known, token: minted.token))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    await #expect(throws: KnobSetupError.wifi(ssid: "home")) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
    #expect(calls.forgets.isEmpty)
}

@Test func checkinMustBeNewerThanTheMintBaseline() {
    let base = Date(timeIntervalSince1970: 1000)
    #expect(!KnobProvisioner.checkedIn(nil, after: nil))
    #expect(KnobProvisioner.checkedIn(base, after: nil))
    #expect(!KnobProvisioner.checkedIn(base, after: base))
    #expect(!KnobProvisioner.checkedIn(base.addingTimeInterval(-1), after: base))
    #expect(KnobProvisioner.checkedIn(base.addingTimeInterval(1), after: base))
}

@Test func provisionPassesTheMintBaselineToTheCheckinTest() async throws {
    var known = minted.device
    let seen = Date(timeIntervalSince1970: 100)
    known.lastCheckin = KnobCheckin(seenAt: seen, fw: "", ip: "", rssi: 0, heapInternalFree: 0,
                                    heapInternalLargest: 0, uptimeS: 0, appliedVersion: 0)
    let box = StateBox("")
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioned))] }
    let mintedKnown = MintedKnob(device: known, token: "ekd_x")
    let p = KnobProvisioner(opener: FakeOpener(knob), mint: { _, _ in mintedKnown },
                            checkedIn: { _, baseline in
                                box.value = baseline.map { "\($0.timeIntervalSince1970)" } ?? "nil"
                                return true
                            }, timeouts: fast)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    _ = try await p.provision(session, identity: id, serialNumber: nil, request: request, progress: { _ in })
    #expect(box.value == "100.0")
}

@Test func identifyRetriesAKnobThatIsStillBooting() async throws {
    let k = FakeKnob()
    let asks = StateBox("0")
    k.onImprov = { m in
        guard case .rpc(ImprovCodec.Command.deviceInfo.rawValue, _) = m else { return [] }
        let n = Int(asks.value)! + 1
        asks.value = "\(n)"
        return n < 2 ? [] : [.bytes(FakeKnob.info)]
    }
    let (_, id) = try await provisioner(FakeOpener(k)).connect(path: "/dev/cu.fake", usbHwID: nil)
    #expect(id.info.isCinder)
    #expect(asks.value == "2")
}

@Test func bootEventCountsAsCinder() async throws {
    let k = FakeKnob()
    k.onImprov = { _ in [.bytes(CinderLineCodec.line(CinderLineCodec.Event(ev: "boot", fw: "0.5.0", provisioned: false)))] }
    let (_, id) = try await provisioner(FakeOpener(k)).connect(path: "/dev/cu.fake", usbHwID: "3cdc7561fc8c")
    #expect(id.info.isCinder)
    #expect(id.info.version == "0.5.0")
}

@Test func expectStopsWhenCancelled() async throws {
    let session = KnobSession(link: FakeKnob())
    await session.start()
    let started = ContinuousClock.now
    let t = Task { try await session.expect(timeout: .seconds(30)) { _ in Optional(1) } }
    try await Task.sleep(for: .milliseconds(20))
    t.cancel()
    await #expect(throws: CancellationError.self) { _ = try await t.value }
    #expect(ContinuousClock.now - started < .seconds(5))
}

@Test func cancelledSetupDoesNotReconnect() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    let opener = FakeOpener(knob, later: [rebootedKnob(ember: "ok")])
    let p = provisioner(opener)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    let t = Task {
        withUnsafeCurrentTask { $0?.cancel() }
        return try await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
    _ = try? await t.value
    #expect(opener.reopenedWith.isEmpty)
}

@Test func wpaPasswordRules() {
    #expect(KnobSetupModel.isValidWPAPassword("hunter22"))
    #expect(!KnobSetupModel.isValidWPAPassword("short"))
    #expect(!KnobSetupModel.isValidWPAPassword(String(repeating: "g", count: 64)))
    #expect(KnobSetupModel.isValidWPAPassword(String(repeating: "a1", count: 32)))
    #expect(try! ImprovCodec.decode(ImprovCodec.state(.stopped)) == .state(.stopped))
}

// MARK: Setup model

@MainActor
private func waitFor(_ model: KnobSetupModel, _ done: (KnobSetupModel.Stage) -> Bool) async {
    for _ in 0..<300 where !done(model.stage) { try? await Task.sleep(for: .milliseconds(10)) }
}

@MainActor
@Test func setupModelAdoptsTheRebootedSessionSoReMintWorks() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    knob.onCinder = { obj in
        let id = obj["id"] as? Int ?? 0
        switch obj["op"] as? String {
        case "info":
            return [FakeKnob.reply(["id": id, "ok": true, "hw_id": "3cdc7561fc8c",
                                    "wifi": ["configured": true], "ember": ["configured": true]])]
        default:
            return [FakeKnob.reply(["id": id, "ok": true])]
        }
    }
    let p = provisioner(FakeOpener(knob, later: [rebootedKnob(ember: "unauthorized", okAfterSetEmber: true)]))
    let port = KnobSerialPort(path: "/dev/cu.fake", vendorID: 0x303A, productID: 0x1001, serialNumber: "3C:DC:75:61:FC:8C")
    let m = KnobSetupModel(mode: .setup, provisioner: p, emberURL: .init(url: "http://192.168.0.2:3627", replacedHost: nil),
                           name: "Desk knob", preferredSSID: "home")
    m.attach(port)
    await waitFor(m) { $0 == .ready }
    await waitFor(m) { _ in !m.networks.isEmpty }
    m.password = "hunter22"
    #expect(m.canSend)
    m.send { _, _ in }
    await waitFor(m) { if case .failed = $0 { true } else { false } }
    #expect(m.stage == .failed(.emberUnauthorized))
    #expect(m.oldTokenRevoked)
    var got: KnobDevice?
    m.remint { d, _ in got = d }
    await waitFor(m) { $0 == .done }
    #expect(m.stage == .done)
    #expect(got?.id == "knob-61fc8c")
    m.close()
}

// MARK: Re-verify gaps

final class TaskBox: @unchecked Sendable {
    private let lock = NSLock()
    private var _task: Task<Void, Never>?
    var task: Task<Void, Never>? {
        get { lock.withLock { _task } }
        set { lock.withLock { _task = newValue } }
    }
}

final class ForgetLog: @unchecked Sendable {
    private let lock = NSLock()
    private var _calls: [(String, Bool)] = []
    var calls: [(String, Bool)] { lock.withLock { _calls } }
    func add(_ id: String, cancelled: Bool) { lock.withLock { _calls.append((id, cancelled)) } }
}

/// A fresh knob whose `set_ember` cancels the setup task.
private func knobCancellingAtSetEmber(_ box: TaskBox) -> FakeKnob {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    let base = knob.onCinder
    knob.onCinder = { obj in
        if obj["op"] as? String == "set_ember" { box.task?.cancel() }
        return base(obj)
    }
    return knob
}

@Test func cancelAfterSetEmberNeitherSendsWiFiNorReconnects() async throws {
    let box = TaskBox()
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    let opener = FakeOpener(knob, later: [rebootedKnob(ember: "ok")])
    let p = provisioner(opener)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    // Cancel as set_ember's reply lands: setEmber succeeds, join must stop.
    await session.observe { e in
        if case .cinder(.reply) = e, knob.received.contains("cinder set_ember") { box.task?.cancel() }
    }
    let t = Task {
        _ = try? await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
    box.task = t
    await t.value
    #expect(knob.received.contains("cinder set_ember"))
    #expect(!knob.received.contains { $0.hasPrefix("improv rpc(command: 1") })
    #expect(opener.reopenedWith.isEmpty)
}

@Test func orphanDeleteSurvivesCancellation() async throws {
    let box = TaskBox()
    let log = ForgetLog()
    let knob = knobCancellingAtSetEmber(box)
    let p = KnobProvisioner(opener: FakeOpener(knob), mint: { _, _ in minted },
                            checkedIn: { _, _ in false },
                            forget: { log.add($0, cancelled: Task.isCancelled) }, timeouts: fast)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    let t = Task {
        _ = try? await p.provision(session, identity: id, serialNumber: "x", request: request, progress: { _ in })
    }
    box.task = t
    await t.value
    #expect(log.calls.map(\.0) == ["knob-61fc8c"])
    #expect(log.calls.first?.1 == false)
}

@Test func remintDropsAStaleEventQueuedAfterTheFailure() async throws {
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioning)), .disconnect] }
    let rebooted = rebootedKnob(ember: "unauthorized", okAfterSetEmber: true)
    let p = provisioner(FakeOpener(knob, later: [rebooted]))
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    let sink = SessionSink()
    await #expect(throws: KnobSetupError.emberUnauthorized) {
        _ = try await p.provision(session, identity: id, serialNumber: "x", request: request,
                                  progress: { _ in }, onSession: { sink.add($0) })
    }
    let live = try #require(sink.all.last)
    // The old token fails again while the user reads the error.
    rebooted.emit(.bytes(CinderLineCodec.line(CinderLineCodec.Event(ev: "ember", state: "unauthorized"))))
    try await Task.sleep(for: .milliseconds(30))
    let (_, device) = try await p.remint(live, identity: id, serialNumber: "x", request: request, progress: { _ in })
    #expect(device.id == "knob-61fc8c")
}

@Test func remintPassesTheMintBaseline() async throws {
    var known = minted.device
    known.lastCheckin = KnobCheckin(seenAt: Date(timeIntervalSince1970: 200), fw: "", ip: "", rssi: 0,
                                    heapInternalFree: 0, heapInternalLargest: 0, uptimeS: 0, appliedVersion: 0)
    let mintedKnown = MintedKnob(device: known, token: "ekd_x")
    let box = StateBox("")
    let knob = freshKnob { _ in [] }
    let p = KnobProvisioner(opener: FakeOpener(knob), mint: { _, _ in mintedKnown },
                            checkedIn: { _, baseline in
                                box.value = baseline.map { "\($0.timeIntervalSince1970)" } ?? "nil"
                                return true
                            }, timeouts: fast)
    let (session, id) = try await p.connect(path: "/dev/cu.fake", usbHwID: nil)
    _ = try await p.remint(session, identity: id, serialNumber: nil, request: request, progress: { _ in })
    #expect(box.value == "200.0")
}

@MainActor
@Test func setupModelKeepsTheReplacedKnobFromSendTime() async throws {
    let old = KnobDevice(id: "knob-old", hwID: "000000000001", name: "Old", createdAt: .distantPast)
    let registry = RegistryBox(old)
    let knob = freshKnob { _ in [.bytes(ImprovCodec.state(.provisioned))] }
    // The mint makes the new board the registered knob, as the pane's reload would.
    let p = KnobProvisioner(opener: FakeOpener(knob), mint: { _, _ in
        await registry.set(minted.device)
        return minted
    }, checkedIn: { _, _ in true }, timeouts: fast)
    let port = KnobSerialPort(path: "/dev/cu.fake", vendorID: 0x303A, productID: 0x1001, serialNumber: nil)
    let m = KnobSetupModel(mode: .setup, provisioner: p, emberURL: .init(url: "http://192.168.0.2:3627", replacedHost: nil),
                           name: "Desk knob", preferredSSID: "home")
    m.registered = { registry.value }
    m.attach(port)
    await waitFor(m) { _ in !m.networks.isEmpty }
    #expect(m.replaces?.id == "knob-old")
    m.password = "hunter22"
    var replaced: KnobDevice?
    m.send { _, old in replaced = old }
    await waitFor(m) { $0 == .done }
    #expect(m.replaces == nil)
    #expect(replaced?.id == "knob-old")
    m.close()
}

@MainActor
final class RegistryBox {
    var value: KnobDevice?
    init(_ v: KnobDevice?) { value = v }
    func set(_ v: KnobDevice?) { value = v }
}
