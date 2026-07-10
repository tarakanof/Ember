import Testing
@testable import EmberKit

@Test func agentMetadataIsStable() {
    #expect(ProducerAgent.claude.binaryName == "ember-claude-producer")
    #expect(ProducerAgent.claude.plistName == "com.ember.heartbeat.plist")
    #expect(ProducerAgent.claude.detectRelPath == ".claude")
    #expect(ProducerAgent.codex.binaryName == "ember-codex-producer")
    #expect(ProducerAgent.codex.plistName == "com.ember.codex.plist")
    #expect(ProducerAgent.codex.detectRelPath == ".codex")
    #expect(ProducerAgent.allCases.count == 2)
}
