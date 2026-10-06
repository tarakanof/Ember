import Testing
import Foundation
@testable import EmberKit

@Test func producerTuningDefaultsMatchTheProducers() {
    let t = ProducerTuning(reading: EnvFile(parsing: ""))
    #expect(!t.codexIncludeClaude)
    #expect(t.codexSources == ["cli", "vscode"])
    #expect(t.codexAppServer)
    #expect(t.doneTTLSeconds == 30)
    #expect(t.claudeAgentsPoll)
    #expect(t.statuslineTimeoutMs == 10_000)
}

@Test(arguments: [
    ("1", true), ("TRUE", true), ("yes", true), ("On", true),
    ("0", false), ("false", false), ("no", false), ("off", false),
    ("maybe", false), ("", false),
])
func codexIncludeClaudeParsesLikeProducerBoolDefaultOff(_ value: String, _ want: Bool) {
    let t = ProducerTuning(reading: EnvFile(parsing: "EMBER_CODEX_INCLUDE_CLAUDE=\(value)\n"))
    #expect(t.codexIncludeClaude == want)
}

@Test(arguments: [
    ("0", false), ("OFF", false), ("no", false), ("false", false),
    ("1", true), ("maybe", true), ("", true),
])
func defaultOnTogglesParseLikeProducerBoolDefaultOn(_ value: String, _ want: Bool) {
    let env = EnvFile(parsing: "EMBER_CODEX_APPSERVER=\(value)\nEMBER_CLAUDE_AGENTS_POLL=\(value)\n")
    let t = ProducerTuning(reading: env)
    #expect(t.codexAppServer == want)
    #expect(t.claudeAgentsPoll == want)
}

@Test(arguments: [
    ("cli,vscode", "cli,vscode"),
    (" CLI , exec,,", "cli,exec"),
    ("mcp", "mcp"),
    ("cli,app", "app,cli"),
    ("", "cli,vscode"),
    (" , ,", "cli,vscode"),
])
func codexSourcesParseLikeParseSources(_ value: String, _ want: String) {
    let t = ProducerTuning(reading: EnvFile(parsing: "EMBER_CODEX_SOURCES=\(value)\n"))
    #expect(t.codexSources.sorted().joined(separator: ",") == want)
}

@Test(arguments: [
    ("45", 45), ("+7", 7), ("0", 30), ("-5", 30), ("ten", 30), ("1.5", 30), ("", 30),
    ("99999999999999999999", 30),
])
func doneTTLParsesAPositiveIntElseTheDefault(_ value: String, _ want: Int) {
    let t = ProducerTuning(reading: EnvFile(parsing: "EMBER_DONE_TTL_SECONDS=\(value)\n"))
    #expect(t.doneTTLSeconds == want)
}

@Test(arguments: [("2500", 2500), ("0", 10_000), ("x", 10_000)])
func statuslineTimeoutParsesAPositiveIntElseTheDefault(_ value: String, _ want: Int) {
    let t = ProducerTuning(reading: EnvFile(parsing: "EMBER_STATUSLINE_TIMEOUT_MS=\(value)\n"))
    #expect(t.statuslineTimeoutMs == want)
}

@Test func applyWritesOnlyChangedKeys() throws {
    let text = "# mine\nEMBER_SOURCE=m4\nEMBER_CODEX_APPSERVER=yes\n"
    var env = EnvFile(parsing: text)
    var t = ProducerTuning(reading: env)
    try t.apply(to: &env)
    #expect(env.serialize() == text)

    t.codexIncludeClaude = true
    t.doneTTLSeconds = 60
    try t.apply(to: &env)
    #expect(env.serialize() == text + "EMBER_CODEX_INCLUDE_CLAUDE=true\nEMBER_DONE_TTL_SECONDS=60\n")
}

@Test func applyWritesEveryKeyTheProducersRead() throws {
    var env = EnvFile(parsing: "")
    var t = ProducerTuning(reading: env)
    t.codexIncludeClaude = true
    t.codexSources = ["mcp", "cli", "exec", "app"]
    t.codexAppServer = false
    t.doneTTLSeconds = 15
    t.claudeAgentsPoll = false
    t.statuslineTimeoutMs = 3000
    try t.apply(to: &env)
    #expect(env.get("EMBER_CODEX_INCLUDE_CLAUDE") == "true")
    #expect(env.get("EMBER_CODEX_SOURCES") == "cli,exec,mcp,app")
    #expect(env.get("EMBER_CODEX_APPSERVER") == "false")
    #expect(env.get("EMBER_DONE_TTL_SECONDS") == "15")
    #expect(env.get("EMBER_CLAUDE_AGENTS_POLL") == "false")
    #expect(env.get("EMBER_STATUSLINE_TIMEOUT_MS") == "3000")
    #expect(ProducerTuning(reading: env) == t)
}

@Test func applyKeepsAnUnknownCodexSourceKind() throws {
    var env = EnvFile(parsing: "EMBER_CODEX_SOURCES=app,cli\n")
    var t = ProducerTuning(reading: env)
    t.setCodexSource("exec", on: true)
    try t.apply(to: &env)
    #expect(env.get("EMBER_CODEX_SOURCES") == "cli,exec,app")
}

@Test func applyRejectsValuesTheProducersWouldIgnore() {
    let base = ProducerTuning(reading: EnvFile(parsing: ""))
    var noSources = base; noSources.codexSources = []
    var zeroTTL = base; zeroTTL.doneTTLSeconds = 0
    var negativeTimeout = base; negativeTimeout.statuslineTimeoutMs = -1
    for bad in [noSources, zeroTTL, negativeTimeout] {
        var env = EnvFile(parsing: "EMBER_SOURCE=m4\n")
        #expect(throws: ValidationError.self) { try bad.apply(to: &env) }
        #expect(env.serialize() == "EMBER_SOURCE=m4\n")
    }
}

@Test func theLastCheckedCodexSourceCannotBeUnchecked() {
    var t = ProducerTuning(reading: EnvFile(parsing: "EMBER_CODEX_SOURCES=cli\n"))
    #expect(!t.canUncheckCodexSource("cli"))
    #expect(t.canUncheckCodexSource("vscode"))
    t.setCodexSource("cli", on: false)
    #expect(t.codexSources == ["cli"])
    t.setCodexSource("mcp", on: true)
    #expect(t.canUncheckCodexSource("cli"))
}

@Test func onlyTheVariablesTheProducersLookUpInTheEnvironmentOverride() {
    let environment = [
        "EMBER_CLAUDE_AGENTS_POLL": "0",
        "EMBER_DONE_TTL_SECONDS": "5",
        "EMBER_CODEX_SOURCES": "mcp",
    ]
    let o = ProducerTuning.Overrides(environment: environment)
    #expect(o.claudeAgentsPoll == false)
    #expect(o.statuslineTimeoutMs == nil)
    #expect(ProducerTuning.Overrides(environment: [:]) == ProducerTuning.Overrides())
}

@Test func anEmptyEnvironmentVariableStillOverridesWithTheDefault() {
    let o = ProducerTuning.Overrides(environment: [
        "EMBER_CLAUDE_AGENTS_POLL": "",
        "EMBER_STATUSLINE_TIMEOUT_MS": "junk",
    ])
    #expect(o.claudeAgentsPoll == true)
    #expect(o.statuslineTimeoutMs == 10_000)
}

@MainActor @Test func producerTuningSavesThroughTheEnvStore() async throws {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true,
                                            attributes: [.posixPermissions: 0o700])
    defer { try? FileManager.default.removeItem(at: dir) }
    let path = dir.appendingPathComponent("producer.env")
    try "# keep me\nEMBER_SOURCE=m4\nEMBER_DONE_TTL_SECONDS=45\n".write(to: path, atomically: true, encoding: .utf8)

    let m = SettingsModels.producerTuningModel(EnvFileStore(path: path))
    await m.load()
    #expect(m.draft.doneTTLSeconds == 45)
    m.draft.claudeAgentsPoll = false
    await m.saveNow()
    #expect(m.status == .saved)
    let text = try String(contentsOf: path, encoding: .utf8)
    #expect(text == "# keep me\nEMBER_SOURCE=m4\nEMBER_DONE_TTL_SECONDS=45\nEMBER_CLAUDE_AGENTS_POLL=false\n")
}

@Test func agentSettingsShowForAnInstalledOrTurnedOnAgent() {
    let snap = ProducerSnapshot(
        agents: [(.claude, .on), (.codex, .off), (.t3, .off)], toggle: .on, undetected: [.codex])
    #expect(snap.showsSettings(for: .claude))
    #expect(!snap.showsSettings(for: .codex))
    #expect(snap.showsSettings(for: .t3))
    let turnedOn = ProducerSnapshot(agents: [(.codex, .on)], toggle: .on, undetected: [.codex])
    #expect(turnedOn.showsSettings(for: .codex))
    #expect(!turnedOn.showsSettings(for: .claude))
}
