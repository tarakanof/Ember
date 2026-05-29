import Testing
import Foundation
@testable import AwtrixMenuKit

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

@Test func fetchSnapshotDecodes() async throws {
    let client = stubbedClient { req in
        #expect(req.url?.path == "/state")
        return (okResponse(req.url!), Data(#"{"sessions":[{"source":"mbp","tool":"claude","session":"s","state":"running","message":""}]}"#.utf8))
    }
    let svc = StatusService(client: client)
    let snap = try await svc.fetchSnapshot()
    #expect(snap.sessions.count == 1)
}
