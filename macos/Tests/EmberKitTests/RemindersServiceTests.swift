import Testing
import Foundation
@testable import EmberKit

@Test func fireSendsJSONToFireEndpoint() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "POST")
        #expect(req.url?.path == "/v1/reminders/fire")
        let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let obj = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        #expect(obj?["text"] as? String == "Walk")
        #expect(obj?["sound"] as? Bool == true)
        #expect(obj?["duration"] as? Int == 8)
        #expect(obj?["native_icon_id"] as? String == "1234")
        return (okResponse(req.url!, status: 204), Data())
    }
    try await RemindersService(client: client).fire(text: "Walk", sound: true, duration: 8, nativeIconId: "1234")
}
