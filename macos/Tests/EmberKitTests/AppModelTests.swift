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

@Test func refreshMarksDisconnectedOnFailure() async throws {
    let client = stubbedClient { req in (okResponse(req.url!, status: 500), Data("boom".utf8)) }
    let model = await AppModel()
    await model.configure(client: client)
    await model.refresh()
    #expect(await model.connected == false)
    #expect(await model.winningSession == nil)
    #expect(await model.sessions.isEmpty)
}
