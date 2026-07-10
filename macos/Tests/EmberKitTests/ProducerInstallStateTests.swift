import Testing
import Foundation
@testable import EmberKit

@MainActor @Test func aggregatePartialWhenOneOnOneOff() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.heartbeat.plist"] = .enabled       // claude on
    sm.statuses["com.ember.codex.plist"] = .notRegistered     // codex off
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })   // both detected
    #expect(svc.toggleState() == .partial)
}

@MainActor @Test func needsApprovalSurfaces() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.heartbeat.plist"] = .requiresApproval
    sm.statuses["com.ember.codex.plist"] = .enabled
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    #expect(svc.agentState(.claude) == .needsApproval)
    #expect(svc.toggleState() == .needsApproval)
}

@MainActor @Test func allOnWhenBothEnabled() {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .enabled, "com.ember.codex.plist": .enabled]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    #expect(svc.toggleState() == .on)
}

@MainActor @Test func allOffWhenBothNotRegistered() {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .notRegistered, "com.ember.codex.plist": .notRegistered]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    #expect(svc.toggleState() == .off)
}

@MainActor @Test func noDetectedAgentsYieldsOff() {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .enabled, "com.ember.codex.plist": .enabled]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in false })   // nothing detected
    #expect(svc.toggleState() == .off)
}

@MainActor @Test func errorTakesPriorityOverNeedsApprovalAndOn() {
    let sm = FakeSMAppService()
    sm.statuses["com.ember.heartbeat.plist"] = .notFound
    sm.statuses["com.ember.codex.plist"] = .requiresApproval
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    #expect(svc.agentState(.claude) == .error("plist not found"))
    #expect(svc.toggleState() == .error)
}
