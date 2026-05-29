import Testing
import Foundation
@testable import AwtrixMenuKit

@Test func buildsClientFromEnv() {
    let env = EnvFile(parsing: "STATUS_SERVER_URL=http://192.168.0.14\nSTATUS_TOKEN=secret\n")
    let c = APIClient(producerEnv: env)
    #expect(c.baseURL == URL(string: "http://192.168.0.14"))
    #expect(c.token == "secret")
}

@Test func nilWhenUrlOrTokenMissing() {
    let c = APIClient(producerEnv: EnvFile(parsing: ""))
    #expect(c.baseURL == nil)
    #expect(c.token == nil)
}

@Test func emptyTokenBecomesNil() {
    let env = EnvFile(parsing: "STATUS_SERVER_URL=http://h\nSTATUS_TOKEN=\n")
    let c = APIClient(producerEnv: env)
    #expect(c.baseURL == URL(string: "http://h"))
    #expect(c.token == nil)
}
