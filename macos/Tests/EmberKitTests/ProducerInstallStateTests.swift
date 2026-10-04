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
