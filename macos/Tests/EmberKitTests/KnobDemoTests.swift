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

    init(statuses: [String]) { _statuses = statuses }

    var requests: [(method: String, path: String, body: [String: Any]?)] { lock.withLock { _requests } }

    func handle(_ req: URLRequest) throws -> (HTTPURLResponse, Data) {
        let path = req.url!.path
        let data = req.httpBody ?? req.httpBodyStreamData()
        let body = data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }
        return lock.withLock {
            _requests.append((req.httpMethod ?? "GET", path, body))
            switch (req.httpMethod, path) {
            case ("GET", "/v1/devices/knob-61fc8c/demo/scenes"):
                return (okResponse(req.url!), Data(demoScenesJSON.utf8))
            case ("POST", "/v1/devices/knob-61fc8c/demo"):
                let scenes = (body?["scenes"] as? [String]) ?? [(body?["scene"] as? String) ?? ""]
                return (okResponse(req.url!), Data(#"{"state":"pending","scene":"\#(scenes[0])","id":"d1","queue_left":\#(scenes.count - 1)}"#.utf8))
            case ("DELETE", "/v1/devices/knob-61fc8c/demo"):
                return (okResponse(req.url!, status: 204), Data())
            case ("GET", "/v1/devices/knob-61fc8c/demo"):
                let state = _statuses.isEmpty ? "idle" : _statuses.removeFirst()
                let scene = state == "idle" ? "" : "bot.hop"
                return (okResponse(req.url!), Data(#"{"state":"\#(state)","scene":"\#(scene)","id":"d1","queue_left":0}"#.utf8))
            default:
                return (okResponse(req.url!, status: 404), Data())
            }
        }
    }
}

@MainActor
private func demoModel(_ server: DemoServer) -> KnobDemoModel {
    let service = KnobService(client: stubbedClient(token: "t") { try server.handle($0) })
    let m = KnobDemoModel(service: service, sleep: { _ in })
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
    #expect(m.error == .featureOff && !m.scenesLoaded)
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
