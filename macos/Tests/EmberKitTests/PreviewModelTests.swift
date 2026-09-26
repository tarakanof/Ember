import Testing
import Foundation
@testable import EmberKit

/// A preview route whose answers the test releases by hand, in any order.
/// It ignores cancellation on purpose: the model must stay latest-wins even
/// when a superseded request still returns.
@MainActor
private final class FakeRoute {
    private(set) var calls: [String] = []
    private var waiting: [String: CheckedContinuation<PreviewResponse, Error>] = [:]

    func fetch(_ tag: String) -> @MainActor () async throws -> PreviewResponse {
        { [self] in
            calls.append(tag)
            return try await withCheckedThrowingContinuation { waiting[tag] = $0 }
        }
    }

    func answer(_ tag: String) async {
        waiting.removeValue(forKey: tag)?.resume(returning: frames(tag))
        await settle()
    }

    func fail(_ tag: String, _ error: Error) async {
        waiting.removeValue(forKey: tag)?.resume(throwing: error)
        await settle()
    }

    private func settle() async { for _ in 0..<50 { await Task.yield() } }
}

private func frames(_ tag: String) -> PreviewResponse {
    PreviewResponse(width: 32, height: 8, activity: tag,
                    frames: [CardFrame(card: tag, pixels: [])])
}

@MainActor
private func makeModel() -> (PreviewModel, ManualClock, FakeRoute) {
    let clock = ManualClock()
    let model = PreviewModel(debounce: .milliseconds(300), sleep: { try await clock.sleep($0) })
    return (model, clock, FakeRoute())
}

@MainActor @Test func previewWaitsForTheDebounce() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("a"))
    await clock.advance(by: .milliseconds(299))
    #expect(route.calls.isEmpty)
    await clock.advance(by: .milliseconds(1))
    #expect(route.calls == ["a"])
    await route.answer("a")
    #expect(m.response == frames("a"))
    #expect(m.error == nil)
}

@MainActor @Test func previewEditsInsideTheDebounceCollapseIntoOneFetch() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("a"))
    await clock.advance(by: .milliseconds(200))
    m.request(route.fetch("b"))
    await clock.advance(by: .milliseconds(200))
    #expect(route.calls.isEmpty)
    await clock.advance(by: .milliseconds(100))
    #expect(route.calls == ["b"])
}

/// Regression for #151: the Agents pane applied whichever response landed
/// last, so a slow older one overwrote the newer preview.
@MainActor @Test func previewSlowOlderResponseNeverOverwritesANewerOne() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("old"))
    await clock.advance(by: .milliseconds(300))
    m.request(route.fetch("new"))
    await clock.advance(by: .milliseconds(300))
    #expect(route.calls == ["old", "new"])
    await route.answer("new")
    await route.answer("old")
    #expect(m.response == frames("new"))
}

@MainActor @Test func previewSlowOlderFailureDoesNotMarkANewerSuccessFailed() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("old"))
    await clock.advance(by: .milliseconds(300))
    m.request(route.fetch("new"))
    await clock.advance(by: .milliseconds(300))
    await route.answer("new")
    await route.fail("old", URLError(.timedOut))
    #expect(m.response == frames("new"))
    #expect(m.error == nil)
}

@MainActor @Test func previewCancelDropsAPendingRequest() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("a"))
    m.cancel()
    await clock.advance(by: .seconds(1))
    #expect(route.calls.isEmpty)
    #expect(clock.sleeperCount == 0)
}

@MainActor @Test func previewCancelDropsAnInFlightOutcome() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("a"))
    await clock.advance(by: .milliseconds(300))
    m.cancel()
    await route.answer("a")
    #expect(m.response == nil)
    #expect(m.error == nil)
}

@MainActor @Test func previewFailureKeepsTheLastGoodResponse() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("a"))
    await clock.advance(by: .milliseconds(300))
    await route.answer("a")
    m.request(route.fetch("b"))
    await clock.advance(by: .milliseconds(300))
    await route.fail("b", URLError(.cannotConnectToHost))
    #expect(m.response == frames("a"))
    #expect(m.error == .offline)
    #expect(!m.isUnavailable)
}

@MainActor @Test func previewMissingRouteIsFeatureOffAndUnavailable() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("a"))
    await clock.advance(by: .milliseconds(300))
    await route.fail("a", APIError.http(status: 404, body: ""))
    #expect(m.response == nil)
    #expect(m.error == .featureOff)
    #expect(m.isUnavailable)
}

@MainActor @Test func previewSuccessClearsTheError() async {
    let (m, clock, route) = makeModel()
    m.request(route.fetch("a"))
    await clock.advance(by: .milliseconds(300))
    await route.fail("a", URLError(.cannotConnectToHost))
    m.request(route.fetch("b"))
    await clock.advance(by: .milliseconds(300))
    await route.answer("b")
    #expect(m.error == nil)
    #expect(m.response == frames("b"))
}

@MainActor @Test func previewFrameLooksUpACardByName() async {
    let (m, clock, route) = makeModel()
    #expect(m.frame("a") == nil)
    m.request(route.fetch("a"))
    await clock.advance(by: .milliseconds(300))
    await route.answer("a")
    #expect(m.frame("a")?.card == "a")
    #expect(m.frame("b") == nil)
}
