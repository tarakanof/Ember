import Testing
import Foundation
@testable import EmberKit

@Test func refreshPopulatesModelFromServices() async throws {
    let client = stubbedClient { req in
        let p = req.url!.path
        let data: String
        switch p {
        case "/state":
            data = #"{"sessions":[{"source":"mbp","tool":"claude","session":"s","state":"running","message":""}]}"#
        case "/v1/pomodoro/state":
            data = #"{"phase":"focus","running":true,"paused":false,"remaining_sec":300,"planned_sec":1500,"round":1}"#
        case "/v1/pomodoro/stats":
            data = #"{"today":{"date":"2026-05-29","completed_focus":2,"focus_min":50},"history":[],"streak":3}"#
        default:
            data = "{}"
        }
        return (okResponse(req.url!), Data(data.utf8))
    }
    let model = await AppModel()
    await model.configure(client: client)
    await model.refresh()

    #expect(await model.connected)
    #expect(await model.winningSession?.tool == "claude")
    #expect(await model.pomoState?.phase == "focus")
    #expect(await model.stats?.streak == 3)
    #expect(await model.sessions.count == 1)
    #expect(await model.sessions.first?.tool == "claude")
}

// Pomodoro endpoints 404 while the feature is disabled on the server. That must
// degrade (no timer/stats) — not blank the dashboard and show Offline while
// /state is perfectly healthy.
@Test func refreshStaysConnectedWhenPomodoroDisabled() async throws {
    let client = stubbedClient { req in
        let p = req.url!.path
        if p.hasPrefix("/v1/pomodoro/") {
            return (okResponse(req.url!, status: 404),
                    Data(#"{"error":"pomodoro feature is not enabled"}"#.utf8))
        }
        if p == "/state" {
            return (okResponse(req.url!),
                    Data(#"{"sessions":[{"source":"mbp","tool":"claude","session":"s","state":"running","message":""}]}"#.utf8))
        }
        return (okResponse(req.url!), Data("{}".utf8))
    }
    let model = await AppModel()
    await model.configure(client: client)
    await model.refresh()

    #expect(await model.connected)
    #expect(await model.sessions.count == 1)
    #expect(await model.pomoState == nil)
    #expect(await model.stats == nil)
}

@Test func refreshMarksDisconnectedOnFailure() async throws {
    let client = stubbedClient { req in (okResponse(req.url!, status: 500), Data("boom".utf8)) }
    let model = await AppModel()
    await model.configure(client: client)
    await model.refresh()
    #expect(await model.connected == false)
    #expect(await model.winningSession == nil)
    #expect(await model.sessions.isEmpty)
}

/// Lock-guarded flag the stub handler (URLProtocol thread) flips for the test.
private final class Flag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false
    func set() { lock.withLock { value = true } }
    var isSet: Bool { lock.withLock { value } }
}

private func sessionJSON(_ tool: String) -> Data {
    Data(#"{"sessions":[{"source":"mbp","tool":"\#(tool)","session":"s","state":"running","message":""}]}"#.utf8)
}

// A slow response from the server the model was configured for *before* a
// Connection-tab change must not land after, and overwrite, the new server's.
@MainActor @Test func staleRefreshFromPreviousServerIsDropped() async throws {
    let started = Flag()
    let gate = DispatchSemaphore(value: 0)
    let oldClient = stubbedClient { req in
        if req.url!.path == "/state" {
            started.set()
            gate.wait()
            return (okResponse(req.url!), sessionJSON("old"))
        }
        return (okResponse(req.url!, status: 404), Data("{}".utf8))
    }
    let newClient = stubbedClient { req in
        if req.url!.path == "/state" { return (okResponse(req.url!), sessionJSON("new")) }
        return (okResponse(req.url!, status: 404), Data("{}".utf8))
    }
    let model = AppModel()
    model.configure(client: oldClient)
    let stale = Task { await model.refresh() }
    while !started.isSet { try await Task.sleep(for: .milliseconds(5)) }

    model.configure(client: newClient)
    await model.refresh()
    #expect(model.sessions.first?.tool == "new")

    gate.signal()
    await stale.value
    #expect(model.sessions.first?.tool == "new")
    #expect(model.connected)
}

@MainActor @Test func setAppFailureIsSurfacedAndClearedOnSuccess() async throws {
    let putsSucceed = Flag()
    let client = stubbedClient { req in
        if req.httpMethod == "PUT", !putsSucceed.isSet {
            return (okResponse(req.url!, status: 401), Data(#"{"error":"unauthorized"}"#.utf8))
        }
        if req.url!.path == "/state" { return (okResponse(req.url!), sessionJSON("claude")) }
        if req.url!.path == "/v1/apps" { return (okResponse(req.url!), Data(#"{"apps":[]}"#.utf8)) }
        return (okResponse(req.url!, status: 404), Data("{}".utf8))
    }
    let model = AppModel()
    model.configure(client: client)
    await model.setApp("claude", enabled: false)
    #expect(model.appToggleError != nil)

    putsSucceed.set()
    await model.setApp("claude", enabled: false)
    #expect(model.appToggleError == nil)
}
