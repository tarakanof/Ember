import Testing
import Foundation
@testable import EmberKit

@MainActor @Test func installAllCollectsPerAgentErrors() async {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    runner.exitFor = { args in args == ["configure"] ? 0 : 0 }
    sm.registerError = nil
    let svc = ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    let outcomes = await svc.installAll()
    #expect(outcomes.count == 3)
    #expect(outcomes.allSatisfy { $0.error == nil })
    #expect(Set(sm.registered) == ["com.ember.heartbeat.plist", "com.ember.codex.plist", "com.ember.t3.plist"])
}

@MainActor @Test func installAllRecordsPerAgentErrorWithoutStoppingOthers() async {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    runner.exitFor = { _ in 0 }
    sm.registerError = nil
    runner.exitFor = { _ in 0 }
    let failingRunner = FailingForCodexRunner()
    let svc = ProducerInstallService(sm: sm, runner: failingRunner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    let outcomes = await svc.installAll()
    #expect(outcomes.count == 3)
    let errored = outcomes.filter { $0.error != nil }
    let clean = outcomes.filter { $0.error == nil }
    #expect(errored.count == 1)
    #expect(errored.first?.agent == .codex)
    #expect(clean.map(\.agent) == [.claude, .t3])
    #expect(sm.registered == ["com.ember.heartbeat.plist", "com.ember.t3.plist"])
}

final class FailingForCodexRunner: ProducerCommandRunning {
    func run(executable: String, arguments: [String]) throws -> CommandResult {
        if executable.hasSuffix("ember-codex-producer") {
            return CommandResult(exitCode: 1, stdout: "", stderr: "")
        }
        return CommandResult(exitCode: 0, stdout: "", stderr: "")
    }
}

@MainActor @Test func uninstallAllUnregistersAllDetected() async {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    let svc = ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    let outcomes = await svc.uninstallAll()
    #expect(outcomes.count == 3)
    #expect(outcomes.allSatisfy { $0.error == nil })
    #expect(Set(sm.unregistered) == ["com.ember.heartbeat.plist", "com.ember.codex.plist", "com.ember.t3.plist"])
}

@MainActor @Test func reconcileReRegistersEnabledOnly() async throws {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .enabled, "com.ember.codex.plist": .notRegistered]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    _ = await svc.reconcile(bundleChanged: true)
    #expect(sm.unregistered == ["com.ember.heartbeat.plist"])
    #expect(sm.registered == ["com.ember.heartbeat.plist"])
}

@MainActor @Test func batchOperationsRunOffTheMainThread() async {
    let runner = FakeRunner()
    let svc = ProducerInstallService(sm: FakeSMAppService(), runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    _ = await svc.installAll()
    _ = await svc.uninstallAll()
    #expect(runner.ranOnMainThread.count == 6)
    #expect(!runner.ranOnMainThread.contains(true))
}

@MainActor @Test func snapshotReportsDetectedAgentsAndAggregate() async {
    let sm = FakeSMAppService()
    sm.statuses = ["com.ember.heartbeat.plist": .enabled]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { $0.hasSuffix("/.claude") })
    let snap = await svc.snapshot()
    #expect(snap.agents.map(\.agent) == [.claude, .t3])
    #expect(snap.agents.map(\.state) == [.on, .off])
    #expect(snap.undetected == [.t3])
    #expect(snap.toggle == .on)
}
