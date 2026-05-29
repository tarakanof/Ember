import Testing
import Foundation
@testable import AwtrixMenuKit

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

// The Go server marshals time.Time as RFC3339 with nanosecond fractional seconds
// (e.g. "2026-05-29T20:33:44.336159758Z"). The decoder must parse both fractional
// and non-fractional ISO8601, or /state decode fails and the app shows offline.
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
