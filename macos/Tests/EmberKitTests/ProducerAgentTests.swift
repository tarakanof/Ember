import Testing
@testable import EmberKit

@Test func agentMetadataIsStable() {
    #expect(ProducerAgent.claude.binaryName == "ember-claude-producer")
    #expect(ProducerAgent.claude.plistName == "com.ember.heartbeat.plist")
    #expect(ProducerAgent.claude.detectRelPath == ".claude")
    #expect(ProducerAgent.codex.binaryName == "ember-codex-producer")
    #expect(ProducerAgent.codex.plistName == "com.ember.codex.plist")
    #expect(ProducerAgent.codex.detectRelPath == ".codex")
    #expect(ProducerAgent.t3.binaryName == "ember-t3-producer")
    #expect(ProducerAgent.t3.plistName == "com.ember.t3.plist")
    #expect(ProducerAgent.t3.label == "com.ember.t3")
    #expect(ProducerAgent.t3.detectRelPath == ".t3")
    #expect(ProducerAgent.allCases.count == 3)
}

@Test func linkStatusPathsMatchTheHelpers() {
    #expect(ProducerAgent.claude.linkStatusRelPath == ".config/ember/claude-producer.link.json")
    #expect(ProducerAgent.codex.linkStatusRelPath == ".config/ember/codex-producer.link.json")
    #expect(ProducerAgent.t3.linkStatusRelPath == ".config/ember/t3-producer.link.json")
    #expect(ProducerAgent.allCases.filter(\.listedWhenUndetected) == [.t3])
}
