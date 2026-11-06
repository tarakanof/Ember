import Testing
import Foundation
@testable import EmberKit

@Test func melodyChoiceFromValue() {
    let names = ["bell", "chime"]
    #expect(MelodyChoice(value: "", available: names) == .builtIn)
    #expect(MelodyChoice(value: "  ", available: names) == .builtIn)
    #expect(MelodyChoice(value: "bell", available: names) == .stored("bell"))
    #expect(MelodyChoice(value: "x:d=4:c", available: names) == .custom)
    #expect(MelodyChoice(value: "gone", available: names) == .custom)
}

@Test func melodyChoiceToValue() {
    let names = ["bell"]
    #expect(MelodyChoice.builtIn.value(replacing: "bell", available: names) == "")
    #expect(MelodyChoice.stored("bell").value(replacing: "", available: names) == "bell")
    #expect(MelodyChoice.custom.value(replacing: "x:d=4:c", available: names) == "x:d=4:c")
    #expect(MelodyChoice.custom.value(replacing: "bell", available: names) == "")
}

@Test func connectionProbeReportsVersion() async {
    let client = stubbedClient(token: "t") { req in
        if req.url?.path == "/version" {
            return (okResponse(req.url!), Data(#"{"version":"0.28.0"}"#.utf8))
        }
        #expect(req.value(forHTTPHeaderField: "Authorization") == "Bearer t")
        return (okResponse(req.url!), Data(#"{"apps":[]}"#.utf8))
    }
    let r = await ConnectionProbe.run(client)
    guard case .connected(let v) = r else { Issue.record("got \(r)"); return }
    #expect(v?.contains("0.28.0") == true)
}

@Test func connectionProbeMapsErrors() async {
    let unauthorized = stubbedClient { req in (okResponse(req.url!, status: 401), Data()) }
    #expect(await ConnectionProbe.run(unauthorized) == .unauthorized)
    let broken = stubbedClient { req in (okResponse(req.url!, status: 500), Data()) }
    #expect(await ConnectionProbe.run(broken) == .serverError(status: 500))
    #expect(await ConnectionProbe.run(APIClient(baseURL: nil, token: nil)) == .notConfigured)
    #expect(ConnectionProbe.result(for: APIError.rateLimited(retryAfter: .seconds(1))) == .rateLimited)
    #expect(ConnectionProbe.result(for: APIError.transport("x")) == .unreachable)
}
