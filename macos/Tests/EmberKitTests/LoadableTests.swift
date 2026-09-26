import Testing
import Foundation
@testable import EmberKit

private let t0 = Date(timeIntervalSince1970: 100)

@Test func loadingHasNoValue() {
    let l = Loadable<Int>.loading
    #expect(l.value == nil)
    #expect(l.loadedAt == nil)
    #expect(l.error == nil)
    #expect(!l.isStale)
    #expect(l.isLoading)
}

@Test func loadedExposesValueAndTime() {
    let l = Loadable.loaded(5, at: t0)
    #expect(l.value == 5)
    #expect(l.loadedAt == t0)
    #expect(!l.isStale)
    #expect(!l.isLoading)
}

@Test func failureAfterAValueKeepsItAsStale() {
    let l = Loadable.loaded(5, at: t0).afterFailure(.offline)
    #expect(l == .failed(.offline, last: 5, lastAt: t0))
    #expect(l.value == 5)
    #expect(l.loadedAt == t0)
    #expect(l.error == .offline)
    #expect(l.isStale)
}

@Test func failureWithoutAValueIsNotStale() {
    let l = Loadable<Int>.loading.afterFailure(.featureOff)
    #expect(l == .failed(.featureOff, last: nil, lastAt: nil))
    #expect(!l.isStale)
    // A second failure keeps the original last value, not nil.
    let twice = Loadable.loaded(1, at: t0).afterFailure(.offline).afterFailure(.unauthorized)
    #expect(twice == .failed(.unauthorized, last: 1, lastAt: t0))
}

@Test func mapKeepsTheState() {
    #expect(Loadable.loaded(2, at: t0).map { $0 * 10 } == .loaded(20, at: t0))
    #expect(Loadable.failed(.offline, last: 2, lastAt: t0).map(String.init) == .failed(.offline, last: "2", lastAt: t0))
    #expect(Loadable<Int>.loading.map { $0 } == .loading)
}

@Test(arguments: [
    (APIError.http(status: 401, body: ""), FeedError.unauthorized),
    (APIError.http(status: 404, body: ""), FeedError.featureOff),
    (APIError.http(status: 405, body: ""), FeedError.featureOff),
    (APIError.rateLimited(retryAfter: .seconds(1)), FeedError.rateLimited),
    (APIError.transport("down"), FeedError.offline),
    (APIError.notConfigured, FeedError.offline),
    (APIError.http(status: 500, body: #"{"error":"boom"}"#), FeedError.server("HTTP 500 — boom")),
    (APIError.decoding("bad"), FeedError.server("Unexpected server response — bad")),
])
func feedErrorMapsAPIErrors(api: APIError, want: FeedError) {
    #expect(FeedError(api) == want)
}

@Test func feedErrorMapsOtherErrors() {
    #expect(FeedError(URLError(.timedOut)) == .offline)
    #expect(FeedError(RequestNotSent(underlying: .transport("x"))) == .offline)
    #expect(FeedError(FeedError.featureOff) == .featureOff)
    #expect(FeedError.unauthorized.localizedDescription.contains("token"))
}
