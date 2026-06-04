import Testing
import Foundation
@testable import EmberKit

@Test func appsServiceDecodesList() async throws {
    let json = #"{"apps":[{"name":"claude","enabled":true},{"name":"codex","enabled":false}]}"#
    let client = stubbedClient(token: "t") { req in
        #expect(req.url?.path == "/v1/apps")
        return (okResponse(req.url!), Data(json.utf8))
    }
    let apps = try await AppsService(client: client).list()
    #expect(apps.count == 2)
    #expect(apps.first(where: { $0.name == "codex" })?.enabled == false)
}

private struct SetAppBody: Decodable { let app: String; let enabled: Bool }

@Test func appsServiceSetSendsCorrectBody() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "PUT")
        #expect(req.url?.path == "/v1/apps")
        let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let obj = try JSONDecoder().decode(SetAppBody.self, from: body)
        #expect(obj.app == "codex")
        #expect(obj.enabled == false)
        return (okResponse(req.url!), Data())
    }
    try await AppsService(client: client).set("codex", enabled: false)
}
