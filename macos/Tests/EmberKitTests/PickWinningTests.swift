import Testing
import Foundation
@testable import EmberKit

private func sess(_ tool: String, _ state: String, _ ago: TimeInterval) -> Session {
    var s = try! JSONDecoder().decode(Session.self, from: Data(#"{"source":"mbp","tool":"x","session":"y","state":"running","message":""}"#.utf8))
    s.tool = tool; s.state = state; s.updatedAt = Date(timeIntervalSince1970: 1_000_000 + ago)
    return s
}

@Test func pickWinningPrefersWaitingThenErrorThenRunningThenDone() {
    let sessions = [sess("a", "done", 100), sess("b", "running", 90), sess("c", "error", 80), sess("d", "waiting", 10)]
    let win = pickWinning(sessions)
    #expect(win?.state == "waiting")
}

@Test func pickWinningPicksMostRecentWithinTopState() {
    let sessions = [sess("a", "running", 10), sess("b", "running", 99), sess("c", "running", 50)]
    let win = pickWinning(sessions)
    #expect(win?.updatedAt == Date(timeIntervalSince1970: 1_000_099))
}

@Test func pickWinningNilWhenEmptyOrAllIdle() {
    #expect(pickWinning([]) == nil)
    #expect(pickWinning([sess("a", "idle", 1)]) == nil)
}

@Test func pickWinningPrefersTheSessionThatEnteredItsStateLast() throws {
    let json = #"{"source":"m4","tool":"claude","session":"%@","state":"waiting","updated_at":"%@","state_since":"%@"}"#
    func decode(_ id: String, updated: String, since: String) throws -> Session {
        let body = String(format: json, id, updated, since)
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return try d.decode(Session.self, from: Data(body.utf8))
    }
    let older = try decode("a", updated: "2026-10-14T10:00:50Z", since: "2026-10-14T09:00:00Z")
    let newer = try decode("b", updated: "2026-10-14T10:00:40Z", since: "2026-10-14T09:30:00Z")
    #expect(pickWinning([older, newer])?.session == "b")
}

@Test func pickWinningFallsBackToUpdatedAtWithoutStateSince() {
    var a = sess("a", "waiting", 10)
    let b = sess("b", "waiting", 20)
    a.stateSince = Date(timeIntervalSince1970: 1_000_500)
    #expect(pickWinning([a, b])?.tool == "b")
}
