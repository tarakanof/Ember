import Testing
import Foundation
@testable import EmberKit

// Issue #142: an enabled agent whose launchd job is gone (booted out, or never
// resubmitted after an update) must be re-registered, and an app update must
// be detected even when CFBundleVersion stays "1".

private let heartbeat = "com.ember.heartbeat.plist"
private let codex = "com.ember.codex.plist"

private func service(_ sm: FakeSMAppService, _ runner: FakeRunner) -> ProducerInstallService {
    ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true }, uid: 501)
}

/// A runner whose `launchctl print` fails (exit 113, "Could not find service")
/// for the given labels.
private func runner(notLoaded labels: Set<String>) -> FakeRunner {
    let r = FakeRunner()
    r.exitFor = { args in
        guard args.first == "print", let target = args.last else { return 0 }
        return labels.contains { target.hasSuffix("/" + $0) } ? 113 : 0
    }
    return r
}

@Test func reconcileReasonMatrix() {
    #expect(reconcileReason(registration: .enabled, loaded: true, bundleChanged: false) == nil)
    #expect(reconcileReason(registration: .enabled, loaded: false, bundleChanged: false) == .notRunning)
    #expect(reconcileReason(registration: .enabled, loaded: true, bundleChanged: true) == .bundleChanged)
    #expect(reconcileReason(registration: .enabled, loaded: false, bundleChanged: true) == .bundleChanged)
    // The user's choice wins: never register an agent that isn't enabled.
    for reg in [AgentRegistration.notRegistered, .requiresApproval, .notFound] {
        #expect(reconcileReason(registration: reg, loaded: false, bundleChanged: true) == nil)
    }
}

@MainActor @Test func livenessProbesLaunchctlPrintForTheAgentsLabel() {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled]
    let r = FakeRunner()
    _ = service(sm, r).agentState(.claude)
    #expect(r.calls.map(\.0) == ["/bin/launchctl"])
    #expect(r.calls.map(\.1) == [["print", "gui/501/com.ember.heartbeat"]])
}

@MainActor @Test func enabledButUnloadedAgentIsNotRunning() {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let svc = service(sm, runner(notLoaded: ["com.ember.heartbeat"]))
    #expect(svc.agentState(.claude) == .notRunning)
    #expect(svc.agentState(.codex) == .on)
    // Reporting is still on (the user's intent); the row carries the problem.
    #expect(svc.toggleState() == .on)
}

@MainActor @Test func agentsThatAreNotEnabledAreNeverProbed() {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .notRegistered, codex: .requiresApproval]
    let r = FakeRunner()
    let svc = service(sm, r)
    #expect(svc.agentState(.claude) == .off)
    #expect(svc.agentState(.codex) == .needsApproval)
    #expect(r.calls.isEmpty)
}

@MainActor @Test func aFailingProbeCountsAsLoaded() {
    // launchctl missing or not runnable: don't churn registrations on a guess.
    final class ThrowingRunner: ProducerCommandRunning {
        func run(executable: String, arguments: [String]) throws -> CommandResult {
            throw NSError(domain: "x", code: 1)
        }
    }
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled]
    let svc = ProducerInstallService(sm: sm, runner: ThrowingRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true }, uid: 501)
    #expect(svc.agentState(.claude) == .on)
}

@MainActor @Test func snapshotFlagsRepairWhenAnAgentIsNotRunning() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let svc = service(sm, runner(notLoaded: ["com.ember.codex"]))
    let snap = await svc.snapshot()
    #expect(snap.needsRepair)
    #expect(snap.agents.map(\.state) == [.on, .notRunning])
}

@MainActor @Test func reconcileReRegistersOnlyTheUnloadedEnabledAgent() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let outcomes = await service(sm, runner(notLoaded: ["com.ember.heartbeat"])).reconcile(bundleChanged: false)
    #expect(sm.unregistered == [heartbeat])
    #expect(sm.registered == [heartbeat])
    #expect(outcomes.map(\.agent) == [.claude])
    #expect(outcomes.map(\.reason) == [.notRunning])
    #expect(outcomes.allSatisfy { $0.error == nil })
}

@MainActor @Test func reconcileAfterAnUpdateCyclesEveryEnabledAgent() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .notRegistered]
    let r = FakeRunner()
    let outcomes = await service(sm, r).reconcile(bundleChanged: true)
    #expect(sm.unregistered == [heartbeat])   // codex is off: left alone
    #expect(sm.registered == [heartbeat])
    #expect(outcomes.map(\.reason) == [.bundleChanged])
    #expect(r.calls.isEmpty)                   // no probe needed when re-registering anyway
}

@MainActor @Test func reconcileWithEverythingHealthyDoesNothing() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let outcomes = await service(sm, FakeRunner()).reconcile(bundleChanged: false)
    #expect(outcomes.isEmpty)
    #expect(sm.registered.isEmpty && sm.unregistered.isEmpty)
}

@MainActor @Test func reconcileReportsAFailedRegistrationAndKeepsGoing() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    sm.registerError = NSError(domain: "x", code: 1)
    let outcomes = await service(sm, FakeRunner()).reconcile(bundleChanged: true)
    #expect(outcomes.map(\.agent) == [.claude, .codex])
    #expect(outcomes.allSatisfy { $0.error != nil })
}

@MainActor @Test func repairAllReRegistersNotRunningAgents() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let outcomes = await service(sm, runner(notLoaded: ["com.ember.codex"])).repairAll()
    #expect(sm.registered == [codex])
    #expect(outcomes.map(\.agent) == [.codex])
}

// MARK: - Bundle fingerprint

@Test func fingerprintChangesWhenOnlyAHelperChanges() {
    let a = producerBundleFingerprint(version: "0.28.0", build: "1", helperDigests: ["aa", "bb"])
    let b = producerBundleFingerprint(version: "0.28.0", build: "1", helperDigests: ["aa", "cc"])
    #expect(a != b)
    #expect(a == producerBundleFingerprint(version: "0.28.0", build: "1", helperDigests: ["aa", "bb"]))
}

@Test func fingerprintChangesWithVersionOrBuild() {
    let base = producerBundleFingerprint(version: "0.28.0", build: "1", helperDigests: ["aa"])
    #expect(base != producerBundleFingerprint(version: "0.28.1", build: "1", helperDigests: ["aa"]))
    #expect(base != producerBundleFingerprint(version: "0.28.0", build: "2", helperDigests: ["aa"]))
    // The old key was the bare CFBundleVersion "1": the new one never equals
    // it, so the first launch of this build reconciles once.
    #expect(shouldReconcileAfterUpdate(currentVersion: base, lastReconciledVersion: "1"))
}

@Test func bundleFingerprintHashesTheBundledHelpersAndPlists() throws {
    let app = FileManager.default.temporaryDirectory.appendingPathComponent("fp-\(UUID().uuidString).app")
    defer { try? FileManager.default.removeItem(at: app) }
    let macos = app.appendingPathComponent("Contents/MacOS")
    let agents = app.appendingPathComponent("Contents/Library/LaunchAgents")
    try FileManager.default.createDirectory(at: macos, withIntermediateDirectories: true)
    try FileManager.default.createDirectory(at: agents, withIntermediateDirectories: true)
    for agent in ProducerAgent.allCases {
        try Data("bin-\(agent)".utf8).write(to: macos.appendingPathComponent(agent.binaryName))
        try Data("plist-\(agent)".utf8).write(to: agents.appendingPathComponent(agent.plistName))
    }
    let first = bundleFingerprint(appURL: app, version: "0.28.0", build: "1")
    #expect(first == bundleFingerprint(appURL: app, version: "0.28.0", build: "1"))

    // Same version and build, rebuilt helper (a new ad-hoc signature): a new key.
    try Data("bin-rebuilt".utf8).write(to: macos.appendingPathComponent(ProducerAgent.claude.binaryName))
    #expect(first != bundleFingerprint(appURL: app, version: "0.28.0", build: "1"))
}
