import Testing
import Foundation
@testable import EmberKit

@Test func parsePreservesCommentsAndOrderAndLastWins() {
    let text = """
    # comment
    EMBER_SOURCE=mbp

    EMBER_SERVER_URL="http://h"
    EMBER_SOURCE=dup
    """
    let env = EnvFile(parsing: text)
    #expect(env.get("EMBER_SOURCE") == "dup")
    #expect(env.get("EMBER_SERVER_URL") == "http://h")
    let out = env.serialize()
    #expect(out.contains("# comment"))
    #expect(out.contains("EMBER_SERVER_URL=http://h"))
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
    env.set("EMBER_SOURCE", "mbp")
    try env.write(to: path)
    let perms = try FileManager.default.attributesOfItem(atPath: path.path)[.posixPermissions] as? NSNumber
    #expect(perms?.int16Value == 0o600)
    #expect(EnvFile(parsing: try String(contentsOf: path, encoding: .utf8)).get("EMBER_SOURCE") == "mbp")
}

@Test func removeDropsEveryOccurrenceAndKeepsTheRest() {
    var env = EnvFile(parsing: "# c\nA=1\nB=2\nA=3\n")
    env.remove("A")
    #expect(env.get("A") == "")
    #expect(env.serialize() == "# c\nB=2\n")
}

@Test func crlfFileParsesLikeGoTrimSpaceAndSavesAsLF() {
    let text = "# note\r\nEMBER_CLAUDE_AGENTS_POLL=0\r\nEMBER_SOURCE=\"mbp\"\r\nEMBER_X=60\r\n"
    var env = EnvFile(parsing: text)
    #expect(env.get("EMBER_CLAUDE_AGENTS_POLL") == "0")
    #expect(!envOn(env.get("EMBER_CLAUDE_AGENTS_POLL")))
    #expect(env.get("EMBER_SOURCE") == "mbp")
    #expect(env.get("EMBER_X") == "60")
    env.set("EMBER_X", "90")
    #expect(env.serialize() == "# note\nEMBER_CLAUDE_AGENTS_POLL=0\nEMBER_SOURCE=mbp\nEMBER_X=90\n")
}

@Test func envTrueAndEnvOnTrimNewlines() {
    #expect(!envTrue("0\n"))
    #expect(!envTrue("false\r\n"))
    #expect(envOn("1\r\n"))
    #expect(envOn("on\n"))
    let off = ProducerTuning.Overrides(environment: [SettingsKeys.claudeAgentsPoll: "0\r\n"])
    #expect(off.claudeAgentsPoll == false)
}

@Test func lineWhoseTrimmedFormStartsWithEqualsIsSkippedLikeGo() {
    let env = EnvFile(parsing: "  =x\nA=1\n")
    #expect(env.get("") == "")
    #expect(env.get("A") == "1")
    #expect(env.serialize() == "  =x\nA=1\n")
}
