import Testing
import Foundation
@testable import EmberKit

private let stuckPrint = """
gui/501/com.ember.heartbeat = {
\tactive count = 0
\tpath = (submitted by smd.339)
\ttype = Submitted
\tmanaged_by = com.apple.xpc.ServiceManagement
\tstate = spawn scheduled

\tprogram identifier = Contents/MacOS/ember-claude-producer (mode: 2)
\tparent bundle identifier = com.ember.Ember
\tBTM uuid = 0DB72F5F-A05B-4F0D-A55E-710EC2D6AA28
\targuments = {
\t\tember-claude-producer
\t\trun
\t}

\tdomain = gui/501 [100021]
\truns = 29
\tlast exit code = 78: EX_CONFIG

\tresource coalition = {
\t\tID = 89013
\t\ttype = resource
\t\tstate = active
\t}

\tspawn type = background (5)
\tjob state = spawn failed

\tproperties = partial import | keepalive | runatload | low priority i/o | resolve program | needs LWCR update | has LWCR
}
"""

private let runningPrint = """
gui/501/com.ember.heartbeat = {
\tactive count = 1
\tpath = (submitted by smd.339)
\ttype = Submitted
\tmanaged_by = com.apple.xpc.ServiceManagement
\tstate = running

\tBTM uuid = 0674FBD2-72A1-4CFD-9D11-0EAFFD9DFD63
\truns = 1
\tpid = 90289
\tlast exit code = (never exited)

\tresource coalition = {
\t\tstate = active
\t}

\tjob state = running
\tproperties = partial import | keepalive | runatload | low priority i/o | resolve program | has LWCR
}
"""

private let crashLoopPrint = """
gui/501/com.ember.heartbeat = {
\tstate = spawn scheduled
\truns = 5
\tlast exit code = 1
\tjob state = exited
\tproperties = partial import | keepalive | runatload | has LWCR
}
"""

private func ok(_ stdout: String) -> CommandResult { CommandResult(exitCode: 0, stdout: stdout, stderr: "") }

@Test func stuckJobFixtureIsStuck() {
    #expect(launchdJobIsStuck(stuckPrint))
    #expect(launchdProbe(ok(stuckPrint)) == .stuck)
}

@Test func runningAndCrashLoopingJobsAreNotStuck() {
    #expect(!launchdJobIsStuck(runningPrint))
    #expect(launchdProbe(ok(runningPrint)) == .loaded)
    #expect(!launchdJobIsStuck(crashLoopPrint))
    #expect(launchdProbe(ok(crashLoopPrint)) == .loaded)
    #expect(launchdProbe(ok("")) == .loaded)
}

@Test func oneSignalAloneIsNotStuck() {
    let lwcrOnly = "x = {\n\tstate = spawn scheduled\n\tproperties = keepalive | needs LWCR update | has LWCR\n}"
    let spawnFailedOnly = "x = {\n\tstate = not running\n\tjob state = spawn failed\n\tlast exit code = 1\n}"
    let exit78Only = "x = {\n\tstate = spawn scheduled\n\tjob state = exited\n\tlast exit code = 78: EX_CONFIG\n}"
    #expect(!launchdJobIsStuck(lwcrOnly))
    #expect(!launchdJobIsStuck(spawnFailedOnly))
    #expect(!launchdJobIsStuck(exit78Only))
}

@Test func spawnFailedWithLWCROrExit78IsStuck() {
    let withLWCR = "x = {\n\tstate = spawn scheduled\n\tjob state = spawn failed\n\tproperties = needs LWCR update\n}"
    let withExit78 = "x = {\n\tstate = spawn scheduled\n\tjob state = spawn failed\n\tlast exit code = 78: EX_CONFIG\n}"
    #expect(launchdJobIsStuck(withLWCR))
    #expect(launchdJobIsStuck(withExit78))
    #expect(!launchdJobIsStuck("x = {\n\tjob state = spawn failed\n\tlast exit code = 780\n}"))
    #expect(!launchdJobIsStuck("x = {\n\tstate = running\n\tjob state = spawn failed\n\tproperties = needs LWCR update\n}"))
}

@Test func printFieldsReadOnlyTheJobsOwnLines() {
    let fields = launchctlPrintFields(stuckPrint)
    #expect(fields["state"] == "spawn scheduled")
    #expect(fields["job state"] == "spawn failed")
    #expect(fields["last exit code"] == "78: EX_CONFIG")
    #expect(fields["ID"] == nil)
}

// MARK: - Service

private let heartbeat = "com.ember.heartbeat.plist"
private let codex = "com.ember.codex.plist"

private func service(_ sm: FakeSMAppService, _ runner: FakeRunner) -> ProducerInstallService {
    ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") }, uid: 501)
}

private func runner(stuck labels: Set<String>) -> FakeRunner {
    let r = FakeRunner()
    r.stdoutFor = { args in
        guard args.first == "print", let target = args.last else { return "" }
        return labels.contains { target.hasSuffix("/" + $0) } ? stuckPrint : runningPrint
    }
    return r
}

@MainActor @Test func aStuckAgentShowsAsNotRunningWithRepair() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let svc = service(sm, runner(stuck: ["com.ember.heartbeat"]))
    #expect(svc.agentState(.claude) == .notRunning)
    #expect(svc.agentState(.codex) == .on)
    let snap = await svc.snapshot()
    #expect(snap.needsRepair)
}

@MainActor @Test func repairBootsOutAStuckJobBeforeRegisteringIt() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let r = runner(stuck: ["com.ember.heartbeat"])
    let outcomes = await service(sm, r).repairAll()
    #expect(outcomes.map(\.agent) == [.claude])
    #expect(outcomes.allSatisfy { $0.error == nil })
    let bootouts: [[String]] = r.calls.map(\.1).filter { $0.first == "bootout" }
    #expect(bootouts == [["bootout", "gui/501/com.ember.heartbeat"]])
    #expect(sm.unregistered == [heartbeat])
    #expect(sm.registered == [heartbeat])
}

@MainActor @Test func reconcileReportsStuckAsItsOwnReason() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let outcomes = await service(sm, runner(stuck: ["com.ember.codex"])).reconcile(bundleChanged: false)
    #expect(outcomes.map(\.agent) == [.codex])
    #expect(outcomes.map(\.reason) == [.stuck])
}

@MainActor @Test func aJobThatIsMerelyNotLoadedIsNotBootedOut() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let r = FakeRunner()
    r.exitFor = { args in args.first == "print" && args.last == "gui/501/com.ember.codex" ? 113 : 0 }
    _ = await service(sm, r).reconcile(bundleChanged: false)
    #expect(!r.calls.contains { $0.1.first == "bootout" })
    #expect(sm.registered == [codex])
}

@MainActor @Test func aFailedBootoutStillRegistersButRepairReportsIt() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled]
    let r = runner(stuck: ["com.ember.heartbeat"])
    r.exitFor = { args in args.first == "bootout" ? 5 : 0 }
    let outcomes = await service(sm, r).repairAll()
    #expect(sm.registered == [heartbeat])
    #expect(outcomes.count == 1)
    let error = outcomes.first?.error as? ProducerInstallError
    #expect(error == .bootoutFailed(exit: 5, detail: ""))
}

@MainActor @Test func bootingOutAJobThatIsAlreadyGoneIsFine() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled]
    let r = runner(stuck: ["com.ember.heartbeat"])
    r.exitFor = { args in args.first == "bootout" ? 3 : 0 }
    let outcomes = await service(sm, r).repairAll()
    #expect(outcomes.allSatisfy { $0.error == nil })
}

@Test func bootoutResultClassification() {
    #expect(bootoutSucceeded(CommandResult(exitCode: 0, stdout: "", stderr: "")))
    #expect(bootoutSucceeded(CommandResult(exitCode: 113, stdout: "", stderr: "")))
    #expect(bootoutSucceeded(CommandResult(exitCode: 3, stdout: "", stderr: "Boot-out failed: 3: No such process")))
    #expect(!bootoutSucceeded(CommandResult(exitCode: 5, stdout: "", stderr: "Boot-out failed: 5: Input/output error")))
    #expect(!bootoutSucceeded(CommandResult(exitCode: 1, stdout: "", stderr: "Operation not permitted")))
}

private final class HealingRunner: ProducerCommandRunning, @unchecked Sendable {
    private let lock = NSLock()
    private var healed = false
    private(set) var bootouts = 0
    func run(executable: String, arguments: [String]) throws -> CommandResult {
        if arguments.first == "bootout" {
            Thread.sleep(forTimeInterval: 0.05)
            lock.withLock { bootouts += 1; healed = true }
            return CommandResult(exitCode: 0, stdout: "", stderr: "")
        }
        let out = lock.withLock { healed } ? runningPrint : stuckPrint
        return CommandResult(exitCode: 0, stdout: out, stderr: "")
    }
}

@Test func concurrentRepairsBootOutOnce() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled]
    let r = HealingRunner()
    let svc = ProducerInstallService(sm: sm, runner: r,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") }, uid: 501)
    async let a = svc.repairAll()
    async let b = svc.repairAll()
    let (first, second) = await (a, b)
    #expect(r.bootouts == 1)
    #expect(first.count + second.count == 1)
}

// MARK: - Local Network

@Test func linkStateDecodesTheHelpersJSON() {
    let blocked = Data(#"{"ok":false,"no_route":true,"error":"dial tcp: connect: no route to host","at":"2026-09-26T16:40:20+02:00"}"#.utf8)
    #expect(ProducerLinkState.decode(blocked) == ProducerLinkState(ok: false, noRoute: true))
    #expect(ProducerLinkState.decode(Data(#"{"ok":true,"no_route":false,"at":"2026-09-26T16:40:20Z"}"#.utf8))
        == ProducerLinkState(ok: true, noRoute: false))
    #expect(ProducerLinkState.decode(Data("garbage".utf8)) == nil)
}

@MainActor @Test func snapshotListsRunningAgentsBlockedFromTheLocalNetwork() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codex: .enabled]
    let files: [String: String] = [
        "/Users/x/.config/ember/claude-producer.link.json": #"{"ok":false,"no_route":true}"#,
        "/Users/x/.config/ember/codex-producer.link.json": #"{"ok":true,"no_route":false}"#,
    ]
    let svc = ProducerInstallService(sm: sm, runner: runner(stuck: []),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") }, readFile: { files[$0].map { Data($0.utf8) } }, uid: 501)
    let snap = await svc.snapshot()
    #expect(snap.localNetworkBlocked == [.claude])
}

@MainActor @Test func aStoppedAgentIsNotReportedAsBlocked() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .notRegistered]
    let svc = ProducerInstallService(sm: sm, runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { !$0.contains("/Library/LaunchAgents/") }, readFile: { _ in Data(#"{"ok":false,"no_route":true}"#.utf8) }, uid: 501)
    let snap = await svc.snapshot()
    #expect(snap.localNetworkBlocked.isEmpty)
}

@Test func recheckRunsOnlyAfterAnUpdateReRegisteredSomething() {
    let ok = ReconcileOutcome(agent: .claude, reason: .bundleChanged, error: nil)
    let failed = ReconcileOutcome(agent: .codex, reason: .bundleChanged, error: NSError(domain: "x", code: 1))
    #expect(shouldRecheckAfterReconcile(bundleChanged: true, outcomes: [ok]))
    #expect(shouldRecheckAfterReconcile(bundleChanged: true, outcomes: [ok, failed]))
    #expect(!shouldRecheckAfterReconcile(bundleChanged: true, outcomes: [failed]))
    #expect(!shouldRecheckAfterReconcile(bundleChanged: true, outcomes: []))
    #expect(!shouldRecheckAfterReconcile(bundleChanged: false, outcomes: [ok]))
}
