import Testing
import Foundation
@testable import EmberKit

/// Lock-guarded state the stub handler (URLProtocol thread) reads and writes.
private final class Server: @unchecked Sendable {
    private let lock = NSLock()
    private var _status = 200
    private var _requests: [String] = []
    private var _bodies: [Data] = []
    var status: Int {
        get { lock.withLock { _status } }
        set { lock.withLock { _status = newValue } }
    }
    var requests: [String] { lock.withLock { _requests } }
    var bodies: [Data] { lock.withLock { _bodies } }
    func log(_ r: URLRequest) {
        lock.withLock {
            _requests.append("\(r.httpMethod ?? "") \(r.url!.path)")
            if let b = r.httpBodyStreamData() ?? r.httpBody { _bodies.append(b) }
        }
    }
}

@MainActor
private func setup(_ server: Server, clock: ManualClock = ManualClock()) -> (ActionRunner, LiveModel) {
    let client = stubbedClient { req in
        server.log(req)
        if req.httpMethod != "GET" { return (okResponse(req.url!, status: server.status), Data()) }
        switch req.url!.path {
        case "/v1/pomodoro/state":
            return (okResponse(req.url!), Data(#"{"phase":"focus","running":true,"paused":false,"remaining_sec":1,"planned_sec":1,"round":1}"#.utf8))
        case "/v1/apps":
            return (okResponse(req.url!), Data(#"{"apps":[]}"#.utf8))
        default:
            return (okResponse(req.url!, status: 404), Data())
        }
    }
    let live = LiveModel(now: { Date(timeIntervalSince1970: 50) }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
    live.configure(client: client)
    let runner = ActionRunner(live: live, connection: ServerConnection(read: { client }), clearAfter: .seconds(10),
                              now: { Date(timeIntervalSince1970: 50) }, sleep: clock.sleepFn)
    return (runner, live)
}

@MainActor @Test func pomodoroActionPostsAndRefreshesTheTimer() async {
    let server = Server()
    let (runner, live) = setup(server)
    let ok = await runner.run(.pomodoro(.start))
    #expect(ok)
    #expect(runner.lastError == nil)
    #expect(server.requests.contains("POST /v1/pomodoro/start"))
    #expect(server.requests.contains("GET /v1/pomodoro/state"))
    // Stats follow a phase change (LiveModelTests), not every action.
    #expect(!server.requests.contains("GET /v1/pomodoro/stats"))
    #expect(live.pomodoro.value?.phaseEnum == .focus)
    #expect(runner.running.isEmpty)
}

@MainActor @Test func failureIsRecordedAndClearedBySuccess() async {
    let server = Server()
    server.status = 401
    let (runner, _) = setup(server)
    let ok = await runner.run(.setApp("claude", enabled: false))
    #expect(!ok)
    #expect(runner.lastError == ActionRunner.Failure(
        action: .setApp("claude", enabled: false), error: .unauthorized, at: Date(timeIntervalSince1970: 50)))
    // The toggle's feed is refreshed either way, so it snaps back.
    #expect(server.requests.contains("GET /v1/apps"))

    server.status = 200
    #expect(await runner.run(.setApp("claude", enabled: false)))
    #expect(runner.lastError == nil)
}

@MainActor @Test func failureClearsItselfAfterTheTimeout() async {
    let server = Server()
    server.status = 404
    let clock = ManualClock()
    let (runner, _) = setup(server, clock: clock)
    await runner.run(.clock(.power(false)))
    #expect(runner.lastError?.error == .featureOff)
    await clock.advance(by: .milliseconds(9_900))
    #expect(runner.lastError != nil)
    await clock.advance(by: .milliseconds(100))
    #expect(runner.lastError == nil)
}

@MainActor @Test func clockActionsHitTheDeviceRoutes() async {
    let server = Server()
    let (runner, _) = setup(server)
    await runner.run(.clock(.next))
    await runner.run(.clock(.previous))
    await runner.run(.clock(.dismiss))
    await runner.run(.clock(.power(true)))
    let posts = server.requests.filter { !$0.hasPrefix("GET") }
    #expect(posts == [
        "POST /v1/device/app/next", "POST /v1/device/app/previous",
        "POST /v1/device/notify/dismiss", "PUT /v1/device/display/power",
    ])
}

@MainActor @Test func everyPomodoroActionPostsToItsRoute() async {
    let server = Server()
    let (runner, _) = setup(server)
    for a in PomodoroAction.allCases { await runner.run(.pomodoro(a)) }
    #expect(server.requests.filter { $0.hasPrefix("POST") } == [
        "POST /v1/pomodoro/start", "POST /v1/pomodoro/pause", "POST /v1/pomodoro/resume",
        "POST /v1/pomodoro/stop", "POST /v1/pomodoro/skip",
    ])
}

private struct SetAppBody: Decodable, Equatable { let app: String; let enabled: Bool }

@MainActor @Test func setAppPutsTheAppAndItsState() async throws {
    let server = Server()
    let (runner, _) = setup(server)
    await runner.run(.setApp("codex", enabled: false))
    #expect(server.requests.contains("PUT /v1/apps"))
    let body = try JSONDecoder().decode(SetAppBody.self, from: try #require(server.bodies.first))
    #expect(body == SetAppBody(app: "codex", enabled: false))
}

@MainActor @Test func actionsFollowTheConnection() async {
    let a = Server(), b = Server()
    let clientA = stubbedClient { req in a.log(req); return (okResponse(req.url!), Data()) }
    let clientB = stubbedClient { req in b.log(req); return (okResponse(req.url!), Data()) }
    var current = clientA
    let connection = ServerConnection(read: { current })
    let runner = ActionRunner(live: LiveModel(), connection: connection)
    await runner.run(.clock(.next))
    current = clientB
    connection.reload()
    await runner.run(.clock(.next))
    #expect(a.requests == ["POST /v1/device/app/next"])
    #expect(b.requests == ["POST /v1/device/app/next"])
}

@MainActor @Test func unconfiguredRunnerFailsOffline() async {
    let clock = ManualClock()
    let live = LiveModel(now: { Date() }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
    let runner = ActionRunner(live: live, connection: ServerConnection(read: { APIClient(baseURL: nil, token: nil) }))
    #expect(await runner.run(.pomodoro(.stop)) == false)
    #expect(runner.lastError?.error == .offline)
}
