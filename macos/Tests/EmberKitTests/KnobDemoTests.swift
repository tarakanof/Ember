import Testing
import Foundation
@testable import EmberKit

private let demoScenesJSON = #"""
[{"name":"bot.idle","group":"Bot","title":"Idle"},
 {"name":"bot.hop","group":"Bot","title":"Hop"},
 {"name":"moment.storm.full","group":"Weather moments","title":"STORM · full"},
 {"name":"face.rain.fog","group":"Weather faces","title":"RAIN · fog"},
 {"name":"pomodoro.focus","group":"Pomodoro","title":"Focus"},
 {"name":"nowplaying.playing","group":"Now playing","title":"Playing"}]
"""#

private final class DemoServer: @unchecked Sendable {
    private let lock = NSLock()
    private var _requests: [(method: String, path: String, body: [String: Any]?)] = []
    private var _statuses: [String]
    private var _gates: [String: DispatchSemaphore] = [:]
    private var _held = 0
    private var _failPost = false
    private var _failScenes = false

    init(statuses: [String], failScenes: Bool = false) { _statuses = statuses; _failScenes = failScenes }

    var failPost: Bool {
        get { lock.withLock { _failPost } }
        set { lock.withLock { _failPost = newValue } }
    }

    var failScenes: Bool {
        get { lock.withLock { _failScenes } }
        set { lock.withLock { _failScenes = newValue } }
    }

    var requests: [(method: String, path: String, body: [String: Any]?)] { lock.withLock { _requests } }
    var statusReads: Int { requests.filter { $0.method == "GET" && $0.path.hasSuffix("/demo") }.count }
    var held: Int { lock.withLock { _held } }

    func holdNext(_ method: String) -> DispatchSemaphore {
        let gate = DispatchSemaphore(value: 0)
        lock.withLock { _gates[method] = gate }
        return gate
    }

    func holdNextStatus() -> DispatchSemaphore { holdNext("GET") }

    func handle(_ req: URLRequest) throws -> (HTTPURLResponse, Data) {
        let path = req.url!.path
        let data = req.httpBody ?? req.httpBodyStreamData()
        let body = data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
        let method = req.httpMethod ?? "GET"
        if path == "/v1/devices/knob-61fc8c/demo",
           let gate: DispatchSemaphore = lock.withLock({
               let g = _gates.removeValue(forKey: method)
               if g != nil { _held += 1 }
               return g
           }) {
            gate.wait()
        }
        return lock.withLock {
            _requests.append((req.httpMethod ?? "GET", path, body))
            switch (req.httpMethod, path) {
            case ("GET", "/v1/devices/knob-61fc8c/demo/scenes"):
                if _failScenes { return (okResponse(req.url!, status: 503), Data()) }
                return (okResponse(req.url!), Data(demoScenesJSON.utf8))
            case ("POST", "/v1/devices/knob-61fc8c/demo"):
                if _failPost { return (okResponse(req.url!, status: 409), Data(#"{"error":"busy"}"#.utf8)) }
                let scenes = (body?["scenes"] as? [String]) ?? [(body?["scene"] as? String) ?? ""]
                return (okResponse(req.url!), Data(#"{"state":"pending","scene":"\#(scenes[0])","id":"d1","queue_left":\#(scenes.count - 1)}"#.utf8))
            case ("DELETE", "/v1/devices/knob-61fc8c/demo"):
                return (okResponse(req.url!, status: 204), Data())
            case ("GET", "/v1/devices/knob-61fc8c/demo"):
                let state = _statuses.isEmpty ? "idle" : _statuses.removeFirst()
                if state == "fail" { return (okResponse(req.url!, status: 503), Data()) }
                let scene = state == "idle" ? "" : "bot.hop"
                return (okResponse(req.url!), Data(#"{"state":"\#(state)","scene":"\#(scene)","id":"d1","queue_left":0}"#.utf8))
            default:
                return (okResponse(req.url!, status: 404), Data())
            }
        }
    }
}

private final class SleepLog: @unchecked Sendable {
    private let lock = NSLock()
    private var _waits: [Duration] = []
    func add(_ d: Duration) { lock.withLock { _waits.append(d) } }
    var waits: [Duration] { lock.withLock { _waits } }
}

@MainActor
private func untilHeld(_ server: DemoServer, _ count: Int = 1) async {
    for _ in 0..<500 where server.held < count { try? await Task.sleep(for: .milliseconds(5)) }
}

@MainActor
private func demoModel(_ server: DemoServer, sleeps: SleepLog? = nil) -> KnobDemoModel {
    let service = KnobService(client: stubbedClient(token: "t") { try server.handle($0) })
    let m = KnobDemoModel(service: service, sleep: { sleeps?.add($0) })
    m.configure(service: service, device: "knob-61fc8c")
    return m
}

private func knob(fw: String?, caps: KnobCaps?) -> KnobDevice {
    var d = KnobDevice(id: "knob-61fc8c", hwID: "a0b1c261fc8c", name: "Knob", createdAt: Date(timeIntervalSince1970: 0),
                       effectiveCaps: caps)
    if let fw {
        d.lastCheckin = KnobCheckin(seenAt: Date(timeIntervalSince1970: 0), fw: fw, ip: "192.0.2.10", rssi: -58,
                                    heapInternalFree: 0, heapInternalLargest: 0, uptimeS: 0, appliedVersion: 1)
    }
    return d
}

@Test func demoNeedsTheDemoTokenWithNoSemverFallback() {
    #expect(KnobDemoAvailability(knob: nil) == .noKnob)
    #expect(KnobDemoAvailability(knob: knob(fw: "9.9.9", caps: nil)) == .needsFirmware)
    #expect(KnobDemoAvailability(knob: knob(fw: "0.9.70", caps: KnobCaps(pages: ["bot"], features: ["wx_moments"]))) == .needsFirmware)
    #expect(KnobDemoAvailability(knob: knob(fw: "0.9.70", caps: KnobCaps(pages: ["bot"], features: [KnobCaps.demo]))) == .ready)
}

@Test func demoScenesGroupInServerOrder() throws {
    let scenes = try JSONDecoder().decode([KnobDemoScene].self, from: Data(demoScenesJSON.utf8))
    let groups = KnobDemoGroup.grouping(scenes)
    #expect(groups.map(\.name) == ["Bot", "Weather moments", "Weather faces", "Pomodoro", "Now playing"])
    #expect(groups[0].scenes.map(\.name) == ["bot.idle", "bot.hop"])
    #expect(KnobDemoGroup.grouping([
        KnobDemoScene(name: "a", group: "X", title: "A"), KnobDemoScene(name: "b", group: "Y", title: "B"),
        KnobDemoScene(name: "c", group: "X", title: "C"),
    ]).map { $0.scenes.map(\.name) } == [["a", "c"], ["b"]])
}

@Test func demoStatusToleratesUnknownStatesAndMissingKeys() throws {
    let d = JSONDecoder()
    #expect(try d.decode(KnobDemoStatus.self, from: Data(#"{"state":"teleporting"}"#.utf8)) == .idle)
    let s = try d.decode(KnobDemoStatus.self, from: Data(#"{"state":"unanswered","scene":"bot.idle","id":"d1","queue_left":3}"#.utf8))
    #expect(s == KnobDemoStatus(state: .unanswered, scene: "bot.idle", id: "d1", queueLeft: 3))
    #expect(!s.isActive)
    #expect(KnobDemoStatus(state: .pending).isActive && KnobDemoStatus(state: .playing).isActive)
}

@MainActor
@Test func demoPlaySendsTheSceneAndHold() async throws {
    let server = DemoServer(statuses: [])
    let m = demoModel(server)
    await m.loadScenes()
    #expect(m.scenesLoaded && m.groups.count == 5)
    m.hold = true
    let follow = m.followID
    await m.play(m.groups[0].scenes[1])
    let post = try #require(server.requests.last)
    #expect(post.method == "POST" && post.path == "/v1/devices/knob-61fc8c/demo")
    #expect(post.body?["scene"] as? String == "bot.hop" && post.body?["hold"] as? Bool == true && post.body?["scenes"] == nil)
    #expect(m.status.state == .pending && m.followID == follow + 1)
    #expect(m.line == .waiting(title: "Hop"))
}

@MainActor
@Test func demoPlayAllSendsTheGroupWithoutHold() async throws {
    let server = DemoServer(statuses: [])
    let m = demoModel(server)
    await m.loadScenes()
    m.hold = true
    await m.playAll(m.groups[0])
    let post = try #require(server.requests.last)
    #expect(post.body?["scenes"] as? [String] == ["bot.idle", "bot.hop"])
    #expect(post.body?["hold"] == nil && post.body?["scene"] == nil)
    #expect(m.status.queueLeft == 1)
}

@MainActor
@Test func demoFollowPollsUntilTheDemoEnds() async {
    let server = DemoServer(statuses: ["pending", "playing", "playing", "idle"])
    let m = demoModel(server)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[1])
    await m.follow()
    #expect(m.status.state == .idle && m.line == .idle)
    #expect(server.requests.filter { $0.method == "GET" && $0.path.hasSuffix("/demo") }.count == 4)
}

@MainActor
@Test func demoFollowStopsOnUnansweredAndSaysSo() async {
    let server = DemoServer(statuses: ["pending", "unanswered", "unanswered"])
    let m = demoModel(server)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[1])
    await m.follow()
    #expect(m.line == .unanswered(title: "Hop"))
    #expect(server.requests.filter { $0.method == "GET" && $0.path.hasSuffix("/demo") }.count == 2)
}

@MainActor
@Test func demoFollowStopsOnARestartedKnobAndSaysSo() async {
    let server = DemoServer(statuses: ["playing", "restarted", "restarted"])
    let m = demoModel(server)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[1])
    await m.follow()
    #expect(m.status.state == .restarted && !m.status.isActive)
    #expect(m.line == .restarted)
    #expect(server.requests.filter { $0.method == "GET" && $0.path.hasSuffix("/demo") }.count == 2)
}

@MainActor
@Test func demoStopSendsDeleteAndGoesIdle() async throws {
    let server = DemoServer(statuses: [])
    let m = demoModel(server)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[0])
    await m.stop()
    let del = try #require(server.requests.last)
    #expect(del.method == "DELETE" && del.path == "/v1/devices/knob-61fc8c/demo")
    #expect(m.status == .idle && m.error == nil && !m.busy)
}

@MainActor
@Test func demoLinesNameTheSceneAndTheQueue() async {
    let m = demoModel(DemoServer(statuses: ["playing"]))
    await m.loadScenes()
    await m.playAll(m.groups[0])
    #expect(m.line == .waiting(title: "Idle"))
    #expect(m.title(of: "no.such") == "no.such")
}

@MainActor
@Test func demoReportsAServerWithoutTheRoutes() async {
    let service = KnobService(client: stubbedClient(token: "t") { req in (okResponse(req.url!, status: 404), Data()) })
    let m = KnobDemoModel(service: service, sleep: { _ in })
    m.configure(service: service, device: "knob-61fc8c")
    await m.loadScenes()
    #expect(m.catalogError == .featureOff && !m.scenesLoaded)
}

@Test func demoRouteIsShownOnlyWithDeveloperTools() {
    let knob = SettingsDevice(id: "knob-61fc8c", kind: .knob, name: "Desk knob", state: .ready)
    let hidden = SettingsTree(devices: [knob])
    #expect(hidden.tools.isEmpty)
    #expect(hidden.resolve(.demo) == .app(.general))
    let shown = SettingsTree(devices: [knob], developerTools: true)
    #expect(shown.tools == [.demo])
    #expect(shown.resolve(.demo) == .demo)
    #expect(shown.expansionIDs(revealing: .demo).isEmpty)
    #expect(SettingsRoute.demo.stored == "tools/demo")
    #expect(SettingsRoute(stored: "tools/demo") == .demo)
    #expect(SettingsRoute(stored: "tools/other") == SettingsRoute.fallback)
    #expect(DeveloperTools.storageKey == "settings.developerTools")
}

@MainActor
@Test func demoStatusReadInFlightDuringPlayIsDropped() async {
    let server = DemoServer(statuses: ["idle"])
    let m = demoModel(server)
    await m.loadScenes()
    let gate = server.holdNextStatus()
    let read = Task { await m.refreshStatus() }
    await untilHeld(server)
    await m.play(m.groups[0].scenes[1])
    gate.signal()
    #expect(await read.value == .superseded)
    #expect(m.status.state == .pending && m.line == .waiting(title: "Hop"))
}

@MainActor
@Test func demoStatusReadInFlightDuringStopIsDropped() async {
    let server = DemoServer(statuses: ["playing"])
    let m = demoModel(server)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[1])
    let gate = server.holdNextStatus()
    let read = Task { await m.refreshStatus() }
    await untilHeld(server)
    await m.stop()
    gate.signal()
    #expect(await read.value == .superseded)
    #expect(m.status == .idle && m.error == nil)
}

@MainActor
@Test func demoCancelledStatusReadIsNotAnError() async {
    let server = DemoServer(statuses: ["playing"])
    let m = demoModel(server)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[1])
    let gate = server.holdNextStatus()
    let read = Task { await m.refreshStatus() }
    await untilHeld(server)
    read.cancel()
    #expect(await read.value == .superseded)
    gate.signal()
    #expect(m.error == nil && m.status.state == .pending)
}

@MainActor
@Test func demoSuccessfulStatusReadClearsTheError() async {
    let server = DemoServer(statuses: ["fail", "playing"])
    let m = demoModel(server)
    await m.loadScenes()
    #expect(await m.refreshStatus() == .failed && m.error != nil)
    #expect(await m.refreshStatus() == .updated && m.error == nil)
    #expect(m.status.state == .playing)
}

@MainActor
@Test func demoFollowRetriesWithBackoffThroughFailures() async {
    let sleeps = SleepLog()
    let server = DemoServer(statuses: ["fail", "fail", "playing", "fail", "idle"])
    let m = demoModel(server, sleeps: sleeps)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[1])
    await m.follow()
    #expect(m.status.state == .idle && m.error == nil)
    #expect(sleeps.waits == [.seconds(1), .seconds(2), .seconds(4), .seconds(1), .seconds(2)])
    #expect(server.statusReads == 5)
}

@MainActor
@Test func demoFollowBackoffIsCapped() async {
    let sleeps = SleepLog()
    let server = DemoServer(statuses: Array(repeating: "fail", count: 7) + ["idle"])
    let m = demoModel(server, sleeps: sleeps)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[1])
    await m.follow()
    #expect(sleeps.waits.max() == KnobDemoModel.maxRetryInterval && m.status.state == .idle)
}

@MainActor
@Test func demoEnteringThePaneReadsTheStatusEvenWithScenesLoaded() async {
    let server = DemoServer(statuses: ["playing", "idle"])
    let m = demoModel(server)
    await m.loadScenes()
    #expect(m.status == .idle)
    await m.enter()
    #expect(server.statusReads == 2 && m.status.state == .idle)
    await m.enter()
    #expect(server.statusReads == 3 && server.requests.filter { $0.path.hasSuffix("/scenes") }.count == 1)
}

@MainActor
@Test func demoEnteringKeepsTheCatalogErrorWhenTheStatusReads() async {
    let server = DemoServer(statuses: ["idle", "idle"], failScenes: true)
    let m = demoModel(server)
    await m.enter()
    #expect(server.statusReads == 1 && m.status.state == .idle && m.error == nil)
    #expect(m.catalogError != nil && !m.scenesLoaded && m.groups.isEmpty)
    server.failScenes = false
    await m.loadScenes()
    #expect(m.catalogError == nil && m.scenesLoaded && m.groups.count == 5)
}

@MainActor
@Test func demoStatusReadDuringAFailedPlayDoesNotHideTheError() async {
    let server = DemoServer(statuses: ["playing"])
    let m = demoModel(server)
    await m.loadScenes()
    server.failPost = true
    let postGate = server.holdNext("POST")
    let play = Task { await m.play(m.groups[0].scenes[1]) }
    await untilHeld(server)
    let readGate = server.holdNextStatus()
    let read = Task { await m.refreshStatus() }
    await untilHeld(server, 2)
    postGate.signal()
    await play.value
    #expect(m.error != nil)
    readGate.signal()
    #expect(await read.value == .superseded)
    #expect(m.error != nil)
}

@MainActor
@Test func demoStatusReadDuringStopCannotLandAStalePlaying() async {
    let server = DemoServer(statuses: ["playing"])
    let m = demoModel(server)
    await m.loadScenes()
    await m.play(m.groups[0].scenes[1])
    let deleteGate = server.holdNext("DELETE")
    let stop = Task { await m.stop() }
    await untilHeld(server)
    let readGate = server.holdNextStatus()
    let read = Task { await m.refreshStatus() }
    await untilHeld(server, 2)
    deleteGate.signal()
    await stop.value
    readGate.signal()
    #expect(await read.value == .superseded)
    #expect(m.status == .idle && m.error == nil)
}
