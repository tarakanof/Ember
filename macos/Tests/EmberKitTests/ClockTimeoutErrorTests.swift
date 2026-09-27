import Testing
import Foundation
@testable import EmberKit

// A clock write that runs out of the server's budget (clockWriteBudget)
// answers 504 {"code":"clock_timeout","write":…}. The app shows it as a slow
// clock with what happened to the change, not as a server error. A read that
// runs out (clockReadBudget) answers the same shape without "write".

@Test(arguments: [
    ("not_sent", ClockWriteOutcome.notSent),
    ("unknown", ClockWriteOutcome.unknown),
    ("applied", ClockWriteOutcome.applied),
    ("something_new", ClockWriteOutcome.unknown),
])
func mapsClockTimeoutToItsOwnError(write: String, want: ClockWriteOutcome) async throws {
    let body = #"{"error":"clock didn't finish within 25s: …","code":"clock_timeout","write":"\#(write)"}"#
    let client = stubbedClient(token: "t") { req in
        (okResponse(req.url!, status: 504), Data(body.utf8))
    }
    await #expect(throws: APIError.clockTimedOut(want)) {
        try await client.put("/v1/device/sensors", body: ["temp_offset": 0], budget: .clockLong)
    }
}

@Test func readClockTimeoutHasNoWriteOutcome() async throws {
    let body = #"{"error":"clock didn't finish within 25s","code":"clock_timeout"}"#
    let client = stubbedClient(token: "t") { req in
        (okResponse(req.url!, status: 504), Data(body.utf8))
    }
    await #expect(throws: APIError.clockTimedOut(nil)) {
        let _: [String: Int] = try await client.get("/v1/device/settings", budget: .clockLong)
    }
}

@Test func otherGatewayTimeoutStaysHTTP() async throws {
    let body = #"{"error":"upstream"}"#
    let client = stubbedClient(token: "t") { req in
        (okResponse(req.url!, status: 504), Data(body.utf8))
    }
    await #expect(throws: APIError.http(status: 504, body: body)) {
        try await client.put("/v1/device/sensors", body: ["temp_offset": 0], budget: .clockLong)
    }
}

@Test func clockTimeoutReadsAsAClockProblemWithItsFate() {
    #expect(FeedError(APIError.clockTimedOut(.notSent)) == .clockTimedOut(.notSent))
    #expect(!FeedError.clockTimedOut(.unknown).isUnreachable)
    #expect(APIError.clockTimedOut(.notSent).localizedDescription
        == "The clock didn't finish in time. Nothing was changed.")
    #expect(APIError.clockTimedOut(.unknown).localizedDescription
        == "The clock didn't finish in time. The change may not have been saved.")
    #expect(FeedError.clockTimedOut(.applied).localizedDescription
        == "The clock didn't finish in time. Saved, but not fully applied yet.")
    #expect(String(localized: FeedError.clockTimedOut(.applied).saveMessage)
        == "The clock didn't finish in time. Saved, but not fully applied yet.")
}

@Test func readClockTimeoutSaysNothingAboutAChange() {
    #expect(FeedError(APIError.clockTimedOut(nil)) == .clockTimedOut(nil))
    #expect(!FeedError.clockTimedOut(nil).isUnreachable)
    #expect(APIError.clockTimedOut(nil).localizedDescription == "The clock didn't finish in time.")
    #expect(String(localized: FeedError.clockTimedOut(nil).saveMessage) == "The clock didn't finish in time.")
}
