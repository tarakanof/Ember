import Testing
import Foundation
@testable import EmberKit

@MainActor @Test func installAllCollectsPerAgentErrors() {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    runner.exitFor = { args in args == ["configure"] ? 0 : 0 }
    sm.registerError = nil
    let svc = ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    let outcomes = svc.installAll()
    #expect(outcomes.count == 2)
    #expect(outcomes.allSatisfy { $0.error == nil })
    #expect(Set(sm.registered) == ["com.ember.heartbeat.plist", "com.ember.codex.plist"])
}

@MainActor @Test func installAllRecordsPerAgentErrorWithoutStoppingOthers() {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    runner.exitFor = { _ in 0 }
    sm.registerError = nil
    // Force the codex configure call to fail, claude to succeed.
    runner.exitFor = { _ in 0 }
    let failingRunner = FailingForCodexRunner()
    let svc = ProducerInstallService(sm: sm, runner: failingRunner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    let outcomes = svc.installAll()
    #expect(outcomes.count == 2)
    let errored = outcomes.filter { $0.error != nil }
    let clean = outcomes.filter { $0.error == nil }
    #expect(errored.count == 1)
    #expect(errored.first?.agent == .codex)
    #expect(clean.count == 1)
    #expect(clean.first?.agent == .claude)
    #expect(sm.registered == ["com.ember.heartbeat.plist"])
}

@MainActor final class FailingForCodexRunner: @preconcurrency ProducerCommandRunning {
    func run(executable: String, arguments: [String]) throws -> CommandResult {
        if executable.hasSuffix("ember-codex-producer") {
            return CommandResult(exitCode: 1, stdout: "", stderr: "")
        }
        return CommandResult(exitCode: 0, stdout: "", stderr: "")
    }
}

@MainActor @Test func uninstallAllUnregistersAllDetected() {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    let svc = ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    let outcomes = svc.uninstallAll()
    #expect(outcomes.count == 2)
    #expect(outcomes.allSatisfy { $0.error == nil })
    #expect(Set(sm.unregistered) == ["com.ember.heartbeat.plist", "com.ember.codex.plist"])
}

@MainActor @Test func reconcileReRegistersEnabledOnly() throws {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .enabled, "com.ember.codex.plist": .notRegistered]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    try svc.reconcileAfterUpdate()
    #expect(sm.unregistered == ["com.ember.heartbeat.plist"])   // only the enabled one cycled
    #expect(sm.registered == ["com.ember.heartbeat.plist"])
}
