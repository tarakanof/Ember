import Testing
import Foundation
@testable import EmberKit

@Test func httpErrorSurfacesServerErrorField() {
    let e = APIError.http(status: 404, body: #"{"error":"pomodoro feature is not enabled"}"#)
    #expect(e.localizedDescription == "HTTP 404 — pomodoro feature is not enabled")
}

@Test func httpErrorFallsBackToBodySnippet() {
    let e = APIError.http(status: 500, body: "boom")
    #expect(e.localizedDescription == "HTTP 500 — boom")
}

@Test func httpErrorWithEmptyBodyShowsStatusOnly() {
    let e = APIError.http(status: 502, body: "  ")
    #expect(e.localizedDescription == "HTTP 502")
}

@Test func notConfiguredPointsAtConnectionSettings() {
    #expect(APIError.notConfigured.localizedDescription
        == "Server not configured — set the server URL in Connection settings.")
}

@Test func transportAndDecodingCarryTheirMessages() {
    #expect(APIError.transport("The request timed out.").localizedDescription
        == "The request timed out.")
    #expect(APIError.decoding("keyNotFound enabled").localizedDescription
        == "Unexpected server response — keyNotFound enabled")
}
