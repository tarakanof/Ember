import Testing
import Foundation
@testable import EmberKit

/// Builds a stub response carrying arbitrary headers (okResponse passes none).
private func response(_ url: URL, status: Int, headers: [String: String]) -> HTTPURLResponse {
    HTTPURLResponse(url: url, statusCode: status, httpVersion: nil, headerFields: headers)!
}

// A 429 is the server throttling this Mac, not a broken clock or a bad token.
// It has to be distinguishable at the call site, or the UI reports the wrong
// cause — which is exactly what the Device tab did ("Clock unreachable").
@Test func mapsRateLimited() async throws {
    let client = stubbedClient(token: "t") { req in
        (response(req.url!, status: 429, headers: ["Retry-After": "3"]),
         Data(#"{"error":"rate limit exceeded"}"#.utf8))
    }
    do {
        let _: DeviceSettings = try await client.get("/v1/device/settings")
        Issue.record("expected a rate-limit error")
    } catch let e as APIError {
        #expect(e.isRateLimited)
        #expect(!e.isUnauthorized)
        #expect(e.retryAfter == .seconds(3))
    }
}

// The server always sends Retry-After, but a proxy in between might not.
@Test func rateLimitedWithoutHeaderStillBacksOff() async throws {
    let client = stubbedClient(token: "t") { req in
        (response(req.url!, status: 429, headers: [:]), Data())
    }
    do {
        let _: DeviceSettings = try await client.get("/v1/device/settings")
        Issue.record("expected a rate-limit error")
    } catch let e as APIError {
        #expect(e.isRateLimited)
        #expect(e.retryAfter == RateLimitBackoff.fallbackRetryAfter)
    }
}

@Test func rateLimitedDescriptionNamesTheCause() {
    let text = APIError.rateLimited(retryAfter: .seconds(2)).errorDescription ?? ""
    #expect(text.lowercased().contains("rate"))
    #expect(!text.lowercased().contains("unreachable"))
}

// Other statuses keep their existing shape — .rateLimited must not swallow them.
@Test func nonRateLimitStatusesStayHTTP() async throws {
    let client = stubbedClient(token: "t") { req in
        (response(req.url!, status: 502, headers: [:]), Data("bad gateway".utf8))
    }
    do {
        let _: DeviceSettings = try await client.get("/v1/device/settings")
        Issue.record("expected an http error")
    } catch let e as APIError {
        #expect(!e.isRateLimited)
        if case .http(let status, _) = e { #expect(status == 502) } else { Issue.record("wrong case: \(e)") }
    }
}

// A poll loop that keeps its cadence through a 429 just keeps being denied: the
// bucket never refills while it is being drained. The pacer is what turns a
// denial into breathing room.
@Test func backoffWaitsAtLeastTheServersRetryAfter() {
    var pacer = RateLimitBackoff(base: .seconds(1))
    #expect(pacer.nextDelay(afterSuccess: true) == .seconds(1))

    let denied = pacer.nextDelay(after: .rateLimited(retryAfter: .seconds(4)))
    #expect(denied >= .seconds(4))
}

@Test func backoffGrowsWhileDeniedAndIsCapped() {
    var pacer = RateLimitBackoff(base: .seconds(1), cap: .seconds(10))
    var last: Duration = .zero
    for _ in 0..<8 {
        let d = pacer.nextDelay(after: .rateLimited(retryAfter: .seconds(1)))
        #expect(d >= last)
        last = d
    }
    #expect(last == .seconds(10))
}

@Test func backoffReturnsToBaseAfterASuccess() {
    var pacer = RateLimitBackoff(base: .seconds(1), cap: .seconds(10))
    _ = pacer.nextDelay(after: .rateLimited(retryAfter: .seconds(4)))
    _ = pacer.nextDelay(after: .rateLimited(retryAfter: .seconds(4)))
    #expect(pacer.nextDelay(afterSuccess: true) == .seconds(1))
}

// A non-429 failure is not the limiter's business: the loop keeps its cadence
// (the mirror already has its own slower retry for an unreachable clock).
@Test func backoffIgnoresOtherFailures() {
    var pacer = RateLimitBackoff(base: .seconds(1))
    #expect(pacer.nextDelay(after: .failed) == .seconds(1))
}
