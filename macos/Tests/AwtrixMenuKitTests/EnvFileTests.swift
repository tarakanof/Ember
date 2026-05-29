import Testing
import Foundation
@testable import AwtrixMenuKit

@Test func parsePreservesCommentsAndOrderAndLastWins() {
    let text = """
    # comment
    STATUS_SOURCE=mbp

    STATUS_SERVER_URL="http://h"
    STATUS_SOURCE=dup
    """
    let env = EnvFile(parsing: text)
    #expect(env.get("STATUS_SOURCE") == "dup")
    #expect(env.get("STATUS_SERVER_URL") == "http://h")
    let out = env.serialize()
    #expect(out.contains("# comment"))
    #expect(out.contains("STATUS_SERVER_URL=http://h"))
}

@Test func setUpdatesLastOccurrenceElseAppends() {
    var env = EnvFile(parsing: "A=1\nA=2\n")
    env.set("A", "9")
    #expect(env.get("A") == "9")
    #expect(env.serialize() == "A=1\nA=9\n")
    env.set("B", "x")
    #expect(env.serialize() == "A=1\nA=9\nB=x\n")
}

@Test func envTrueDefaultsTrueEnvOnDefaultsFalse() {
    #expect(envTrue(""))
    #expect(envTrue("yes"))
    #expect(!envTrue("false"))
    #expect(!envTrue("0"))
    #expect(!envOn(""))
    #expect(envOn("1"))
    #expect(envOn("on"))
    #expect(!envOn("nope"))
}

@Test func writeAtomicCreates0600() throws {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true,
        attributes: [.posixPermissions: 0o700])
    let path = dir.appendingPathComponent("producer.env")
    var env = EnvFile(parsing: "")
    env.set("STATUS_SOURCE", "mbp")
    try env.write(to: path)
    let perms = try FileManager.default.attributesOfItem(atPath: path.path)[.posixPermissions] as? NSNumber
    #expect(perms?.int16Value == 0o600)
    #expect(EnvFile(parsing: try String(contentsOf: path, encoding: .utf8)).get("STATUS_SOURCE") == "mbp")
}
