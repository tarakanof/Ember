import Testing
import Foundation
@testable import EmberKit

/// Stand-in for producer.env: the test rewrites it between reloads.
@MainActor
private final class EnvText {
    var text = ""
    func client() -> APIClient { APIClient(producerEnv: EnvFile(parsing: text)) }
}

@MainActor @Test func connectionReadsTheEnvAtInit() {
    let env = EnvText()
    env.text = "EMBER_SERVER_URL=http://a.test:3627\nEMBER_TOKEN=t\n"
    let c = ServerConnection(read: env.client)
    #expect(c.serverURL == URL(string: "http://a.test:3627"))
    #expect(c.client.token == "t")
}

@MainActor @Test func missingEnvIsUnconfigured() {
    let c = ServerConnection(envPath: URL(fileURLWithPath: "/nonexistent/producer.env"))
    #expect(c.serverURL == nil)
}

@MainActor @Test func reloadReportsAServerOrTokenChange() {
    let env = EnvText()
    env.text = "EMBER_SERVER_URL=http://a.test:3627\nEMBER_TOKEN=t\n"
    let c = ServerConnection(read: env.client)

    // A source-name save: same URL and token, nothing to re-point.
    env.text += "EMBER_SOURCE=mbp\n"
    #expect(!c.reload())

    env.text = "EMBER_SERVER_URL=http://a.test:3627\nEMBER_TOKEN=u\n"
    #expect(c.reload())
    #expect(c.client.token == "u")

    env.text = "EMBER_SERVER_URL=http://b.test:3627\nEMBER_TOKEN=u\n"
    #expect(c.reload())
    #expect(c.serverURL == URL(string: "http://b.test:3627"))

    env.text = ""
    #expect(c.reload())
    #expect(c.serverURL == nil)
}
