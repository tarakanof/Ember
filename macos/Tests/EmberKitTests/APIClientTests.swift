import Testing
import Foundation
@testable import EmberKit

@Test func getDecodesAndSendsBearer() async throws {
    let client = stubbedClient(token: "secret") { req in
        #expect(req.value(forHTTPHeaderField: "Authorization") == "Bearer secret")
        #expect(req.url?.path == "/v1/pomodoro/state")
        let body = #"{"phase":"focus","running":true,"paused":false,"remaining_sec":10,"planned_sec":25,"round":1}"#
        return (okResponse(req.url!), Data(body.utf8))
    }
    let st: PomoState = try await client.get("/v1/pomodoro/state")
    #expect(st.phase == "focus")
}

@Test func noAuthHeaderWhenTokenNil() async throws {
    let client = stubbedClient(token: nil) { req in
        #expect(req.value(forHTTPHeaderField: "Authorization") == nil)
        return (okResponse(req.url!), Data("{\"sessions\":[]}".utf8))
    }
    let _: Snapshot = try await client.get("/state")
}

@Test func mapsUnauthorized() async throws {
    let client = stubbedClient(token: "bad") { req in
        (okResponse(req.url!, status: 401), Data("unauthorized".utf8))
    }
    await #expect(throws: APIError.self) {
        let _: PomoConfig = try await client.get("/v1/pomodoro/config")
    }
    do {
        let _: PomoConfig = try await client.get("/v1/pomodoro/config")
    } catch let e as APIError {
        #expect(e.isUnauthorized)
    }
}

@Test func notConfiguredWhenNoBaseURL() async throws {
    let client = APIClient(baseURL: nil, token: nil, session: .shared)
    await #expect(throws: APIError.notConfigured) {
        let _: Snapshot = try await client.get("/state")
    }
}

@Test func decodesFractionalSecondTimestamps() async throws {
    let client = stubbedClient { req in
        let body = #"{"sessions":[{"source":"mbp","tool":"claude","session":"s","state":"running","message":"","updated_at":"2026-05-29T20:33:44.336159758Z"}]}"#
        return (okResponse(req.url!), Data(body.utf8))
    }
    let snap: Snapshot = try await client.get("/state")
    #expect(snap.sessions.first?.state == "running")
}

@Test func decodesNonFractionalTimestamps() async throws {
    let client = stubbedClient { req in
        let body = #"{"sessions":[{"source":"mbp","tool":"claude","session":"s","state":"done","message":"","updated_at":"2026-05-29T12:00:00Z"}]}"#
        return (okResponse(req.url!), Data(body.utf8))
    }
    let snap: Snapshot = try await client.get("/state")
    #expect(snap.sessions.first?.state == "done")
}

@Test func conditionalGetDecodesAndReturnsTheTagAndCap() async throws {
    let client = stubbedClient { req in
        let resp = HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: nil,
                                   headerFields: ["ETag": #"W/"a""#, "X-Ember-View-Wait": "25"])!
        return (resp, Data(#"{"sessions":[]}"#.utf8))
    }
    let reply: ConditionalReply<Snapshot> = try await client.getConditional("/state", etag: nil, wait: 25)
    #expect(reply.value == Snapshot(sessions: []))
    #expect(reply.etag == #"W/"a""#)
    #expect(reply.waitCap == 25)
    #expect(reply.supportsWait)
}

@Test func conditionalGetTurns304IntoNoValueAndKeepsTheSentTag() async throws {
    let client = stubbedClient { req in
        #expect(req.value(forHTTPHeaderField: "If-None-Match") == #"W/"a""#)
        #expect(req.url?.query == "wait=25")
        return (okResponse(req.url!, status: 304), Data())
    }
    let reply: ConditionalReply<Snapshot> = try await client.getConditional("/state", etag: #"W/"a""#, wait: 25)
    #expect(reply.value == nil)
    #expect(reply.etag == #"W/"a""#)
}

@Test func conditionalGetWithoutATagNeverWaits() async throws {
    let client = stubbedClient { req in
        #expect(req.url?.query == nil)
        return (okResponse(req.url!), Data(#"{"sessions":[]}"#.utf8))
    }
    let reply: ConditionalReply<Snapshot> = try await client.getConditional("/state", etag: nil, wait: 25)
    #expect(!reply.supportsWait)
}

@Test func aPlainGetStillTreats304AsAnError() async {
    let client = stubbedClient { req in (okResponse(req.url!, status: 304), Data()) }
    await #expect(throws: APIError.self) {
        let _: Snapshot = try await client.get("/state")
    }
}

@Test func onlyAWaitingRequestUsesTheLongPollBudget() async throws {
    let budgets = BudgetLog()
    let stub = stubbedClient { req in (okResponse(req.url!, status: 304), Data()) }
    let client = APIClient(baseURL: stub.baseURL, token: nil, sessions: { budget in
        budgets.append(budget)
        return stub.sessions(budget)
    }, pathStatus: { nil })
    let _: ConditionalReply<Snapshot> = try await client.getConditional("/state", etag: "x", wait: 25)
    let _: ConditionalReply<Snapshot> = try await client.getConditional("/state", etag: "x")
    #expect(budgets.all == [.longPoll, .server])
    #expect(RequestBudget.longPoll.requestTimeout > 25)
}

private final class BudgetLog: @unchecked Sendable {
    private let lock = NSLock()
    private var log: [RequestBudget] = []
    var all: [RequestBudget] { lock.withLock { log } }
    func append(_ b: RequestBudget) { lock.withLock { log.append(b) } }
}
