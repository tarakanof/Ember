import Testing
import Foundation
@testable import EmberKit

@MainActor @Test func aggregatePartialWhenOneOnOneOff() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.heartbeat.plist"] = .enabled
    sm.statuses["com.ember.codex.plist"] = .notRegistered
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") })
    #expect(svc.toggleState() == .partial)
}

@MainActor @Test func needsApprovalSurfaces() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.heartbeat.plist"] = .requiresApproval
    sm.statuses["com.ember.codex.plist"] = .enabled
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") })
    #expect(svc.agentState(.claude) == .needsApproval)
    #expect(svc.toggleState() == .needsApproval)
}

@MainActor @Test func allOnWhenBothEnabled() {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .enabled, "com.ember.codex.plist": .enabled,
                   "com.ember.t3.plist": .enabled]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") })
    #expect(svc.toggleState() == .on)
}

@MainActor @Test func allOffWhenBothNotRegistered() {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .notRegistered, "com.ember.codex.plist": .notRegistered]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") })
    #expect(svc.toggleState() == .off)
}

@MainActor @Test func noDetectedOrRegisteredAgentsYieldsOff() {
    let svc = ProducerInstallService(sm: FakeSMAppService(), runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in false })
    #expect(svc.toggleState() == .off)
}

@MainActor @Test func registeredAgentsCountEvenWithoutTheirTool() {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .enabled, "com.ember.codex.plist": .enabled]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in false })
    #expect(svc.toggleState() == .on)
}

@MainActor @Test func errorTakesPriorityOverNeedsApprovalAndOn() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.heartbeat.plist"] = .notFound
    sm.statuses["com.ember.codex.plist"] = .requiresApproval
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") })
    #expect(svc.agentState(.claude) == .error("Not installed: the app is missing its launch agent."))
    #expect(svc.toggleState() == .error)
}

private func notFoundService(_ sm: FakeSMAppService, runner: FakeRunner = FakeRunner(),
                             existing: Set<String>) -> ProducerInstallService {
    ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { existing.contains($0) })
}

private let bundledT3 = "/A/Contents/Library/LaunchAgents/com.ember.t3.plist"
private let cliT3 = "/Users/x/Library/LaunchAgents/com.ember.t3.plist"

@MainActor @Test func notFoundWithBundledPlistIsOff() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.t3.plist"] = .notFound
    #expect(notFoundService(sm, existing: [bundledT3]).agentState(.t3) == .off)
}

@MainActor @Test func notFoundWithBundledAndCLIPlistIsCLIInstalled() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.t3.plist"] = .notFound
    #expect(notFoundService(sm, existing: [bundledT3, cliT3]).agentState(.t3) == .cliInstalled)
}

@MainActor @Test func notFoundWithoutBundledPlistIsError() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.t3.plist"] = .notFound
    #expect(notFoundService(sm, existing: [cliT3]).agentState(.t3)
        == .error("Not installed: the app is missing its launch agent."))
}

@MainActor @Test func toggleIsNotErrorWhenTheNotFoundAgentIsBundled() {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .enabled, "com.ember.t3.plist": .notFound]
    let svc = notFoundService(sm, existing: [bundledT3, "/Users/x/.t3"])
    #expect(svc.toggleState() == .partial)
}

@MainActor @Test func turningOnABundledNotFoundAgentRegistersIt() async {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.t3.plist"] = .notFound
    let outcomes = await notFoundService(sm, existing: [bundledT3]).setEnabled(.t3, true)
    #expect(outcomes.allSatisfy { $0.error == nil })
    #expect(sm.registered == ["com.ember.t3.plist"])
}

@MainActor @Test func turningOnABundledNotFoundAgentWithACLICopyRefusesToRegister() async {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.t3.plist"] = .notFound
    let outcomes = await notFoundService(sm, existing: [bundledT3, cliT3]).setEnabled(.t3, true)
    #expect(outcomes.map { $0.error as? ProducerInstallError } == [.cliInstalled])
    #expect(sm.registered.isEmpty)
}

@Test func switchIsOnOnlyForOnLikeStates() {
    #expect(AgentState.on.isSwitchOn)
    #expect(AgentState.notRunning.isSwitchOn)
    #expect(AgentState.needsApproval.isSwitchOn)
    #expect(!AgentState.off.isSwitchOn)
    #expect(!AgentState.cliInstalled.isSwitchOn)
    #expect(!AgentState.error("x").isSwitchOn)
}

@Test func reportingExcludesOffAndErrorRows() {
    #expect(AgentState.on.isReporting)
    #expect(AgentState.notRunning.isReporting)
    #expect(AgentState.needsApproval.isReporting)
    #expect(AgentState.cliInstalled.isReporting)
    #expect(!AgentState.off.isReporting)
    #expect(!AgentState.error("x").isReporting)
}
