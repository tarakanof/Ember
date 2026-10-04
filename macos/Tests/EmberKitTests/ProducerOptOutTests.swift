import Testing
import Foundation
@testable import EmberKit

private let heartbeat = "com.ember.heartbeat.plist"
private let codexPlist = "com.ember.codex.plist"
private let t3Plist = "com.ember.t3.plist"
private let allTools: Set<String> = ["/Users/x/.claude", "/Users/x/.codex", "/Users/x/.t3"]

private func service(_ sm: FakeSMAppService, runner: ProducerCommandRunning = FakeRunner(),
                     dirs: Set<String> = allTools, files: [String: String] = [:],
                     prefs: InMemoryProducerPrefs = InMemoryProducerPrefs()) -> ProducerInstallService {
    ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { dirs.contains($0) || files[$0] != nil },
        readFile: { files[$0].map { Data($0.utf8) } },
        prefs: prefs, uid: 501)
}

@MainActor @Test func turningOneAgentOffLeavesTheMasterSwitchOn() async {
    let sm = FakeSMAppService()
    sm.statuses = [heartbeat: .enabled, codexPlist: .enabled, t3Plist: .enabled]
    let prefs = InMemoryProducerPrefs()
    let svc = service(sm, prefs: prefs)
    _ = await svc.setEnabled(.codex, false)
    #expect(prefs.optOut == ["codex"])
    #expect(svc.managedAgents() == [.claude, .t3])
    #expect(svc.toggleState() == .on)
}

@MainActor @Test func masterOnSkipsOptedOutAgentsAndPerAgentOnClearsTheOptOut() async {
    let sm = FakeSMAppService()
    let prefs = InMemoryProducerPrefs(optOut: ["t3"])
    let svc = service(sm, prefs: prefs)
    _ = await svc.installAll()
    #expect(Set(sm.registered) == [heartbeat, codexPlist])
    _ = await svc.setEnabled(.t3, true)
    #expect(prefs.optOut.isEmpty)
    #expect(sm.registered.last == t3Plist)
}

@MainActor @Test func masterOnWithEveryDetectedAgentOptedOutTurnsThemAllOn() async {
    let sm = FakeSMAppService()
    let prefs = InMemoryProducerPrefs(optOut: ["claude", "codex"])
    let svc = service(sm, dirs: ["/Users/x/.claude", "/Users/x/.codex"], prefs: prefs)
    #expect(svc.toggleState() == .off)
    _ = await svc.installAll()
    #expect(Set(sm.registered) == [heartbeat, codexPlist])
    #expect(prefs.optOut.isEmpty)
}

@MainActor @Test func upgradeSeedsTheNewAgentAsOptedOutWhenReportingWasOn() async {
    let sm = FakeSMAppService(); sm.statuses = [heartbeat: .enabled, codexPlist: .enabled]
    let prefs = InMemoryProducerPrefs()
    let svc = service(sm, prefs: prefs)
    await svc.seedOptOutForNewAgents()
    #expect(prefs.optOut == ["t3"])
    #expect(prefs.knownAgents == ["claude", "codex", "t3"])
    #expect(svc.toggleState() == .on)
    prefs.optOut = []
    await svc.seedOptOutForNewAgents()
    #expect(prefs.optOut.isEmpty)
}

@MainActor @Test func aFreshInstallSeedsNothing() async {
    let prefs = InMemoryProducerPrefs()
    await service(FakeSMAppService(), prefs: prefs).seedOptOutForNewAgents()
    #expect(prefs.optOut.isEmpty)
    #expect(prefs.knownAgents == ["claude", "codex", "t3"])
}

@MainActor @Test func aCLIInstalledAgentIsShownAndNeverRegisteredTwice() async throws {
    let sm = FakeSMAppService()
    let cli = "/Users/x/Library/LaunchAgents/com.ember.t3.plist"
    let svc = service(sm, files: [cli: "<plist/>"])
    #expect(svc.agentState(.t3) == .cliInstalled)
    #expect(!svc.managedAgents().contains(.t3))
    _ = await svc.installAll()
    #expect(!sm.registered.contains(t3Plist))
    #expect(throws: ProducerInstallError.cliInstalled) { try svc.install(.t3) }
    let snap = await svc.snapshot()
    #expect(snap.agents.first { $0.agent == .t3 }?.state == .cliInstalled)
}

@MainActor @Test func movingACLIAgentToEmberUninstallsTheCLICopyFirst() async {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    let cli = "/Users/x/Library/LaunchAgents/com.ember.t3.plist"
    let gone = FlagBox()
    let svc = ProducerInstallService(sm: sm, runner: RemovingRunner(inner: runner, onUninstall: { gone.set() }),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { $0 == cli ? !gone.value : allTools.contains($0) },
        readFile: { _ in nil }, prefs: InMemoryProducerPrefs(), uid: 501)
    let outcomes = await svc.moveToEmber(.t3)
    #expect(outcomes.allSatisfy { $0.error == nil })
    #expect(runner.calls.map(\.1) == [["uninstall"], ["configure"]])
    #expect(runner.calls.first?.0 == "/A/Contents/MacOS/ember-t3-producer")
    #expect(sm.registered == [t3Plist])
}

@MainActor @Test func movingFailsWhenTheCLICopyStays() async {
    let sm = FakeSMAppService()
    let cli = "/Users/x/Library/LaunchAgents/com.ember.t3.plist"
    let outcomes = await service(sm, files: [cli: "x"]).moveToEmber(.t3)
    #expect(outcomes.first?.error as? ProducerInstallError == .cliInstalled)
    #expect(sm.registered.isEmpty)
}

@Test func configureFailureCarriesTheHelpersMessage() {
    let error = ProducerInstallError.configureFailed(exit: 1, detail: "settings.json is not valid JSON")
    #expect(error.localizedDescription.contains("settings.json is not valid JSON"))
}

@MainActor @Test func installReportsConfigureStderr() {
    final class StderrRunner: ProducerCommandRunning {
        func run(executable: String, arguments: [String]) throws -> CommandResult {
            CommandResult(exitCode: 1, stdout: "", stderr: "configure failed: boom\n")
        }
    }
    let svc = service(FakeSMAppService(), runner: StderrRunner())
    #expect(throws: ProducerInstallError.configureFailed(exit: 1, detail: "configure failed: boom")) {
        try svc.install(.claude)
    }
}

final class FlagBox: @unchecked Sendable {
    private let lock = NSLock()
    private var flag = false
    var value: Bool { lock.withLock { flag } }
    func set() { lock.withLock { flag = true } }
}

final class RemovingRunner: ProducerCommandRunning, @unchecked Sendable {
    let inner: FakeRunner
    let onUninstall: @Sendable () -> Void
    init(inner: FakeRunner, onUninstall: @escaping @Sendable () -> Void) {
        self.inner = inner
        self.onUninstall = onUninstall
    }
    func run(executable: String, arguments: [String]) throws -> CommandResult {
        if arguments == ["uninstall"] { onUninstall() }
        return try inner.run(executable: executable, arguments: arguments)
    }
}

private let cliT3 = "/Users/x/Library/LaunchAgents/com.ember.t3.plist"
private let cliPlistBody = """
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>Label</key><string>com.ember.t3</string>
<key>ProgramArguments</key><array><string>/Users/x/go/bin/ember-t3-producer</string><string>run</string></array></dict></plist>
"""

final class ScriptedRunner: ProducerCommandRunning, @unchecked Sendable {
    private let lock = NSLock()
    private var _calls: [(String, [String])] = []
    let result: @Sendable (String, [String]) -> CommandResult
    init(_ result: @escaping @Sendable (String, [String]) -> CommandResult) { self.result = result }
    var calls: [(String, [String])] { lock.withLock { _calls } }
    func run(executable: String, arguments: [String]) throws -> CommandResult {
        lock.withLock { _calls.append((executable, arguments)) }
        return result(executable, arguments)
    }
}

private func moveService(_ sm: FakeSMAppService, _ runner: ScriptedRunner, cliGone: FlagBox,
                         extraFiles: [String: String] = [:]) -> ProducerInstallService {
    ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { path in
            if path == cliT3 { return !cliGone.value }
            return path == "/Users/x/go/bin/ember-t3-producer" || allTools.contains(path) || extraFiles[path] != nil
        },
        readFile: { path in path == cliT3 ? Data(cliPlistBody.utf8) : extraFiles[path].map { Data($0.utf8) } },
        prefs: InMemoryProducerPrefs(), uid: 501)
}

@MainActor @Test func aFailedCLIUninstallAbortsTheMove() async {
    let sm = FakeSMAppService()
    let runner = ScriptedRunner { _, args in
        args == ["uninstall"] ? CommandResult(exitCode: 1, stdout: "", stderr: "uninstall: boom\n")
            : CommandResult(exitCode: 0, stdout: "", stderr: "")
    }
    let outcomes = await moveService(sm, runner, cliGone: FlagBox()).moveToEmber(.t3)
    #expect(outcomes.first?.error as? ProducerInstallError == .cliUninstallFailed(exit: 1, detail: "uninstall: boom"))
    #expect(runner.calls.map(\.1) == [["uninstall"]])
    #expect(sm.registered.isEmpty)
}

@MainActor @Test func aFailedTakeoverReinstallsTheCLIAgent() async {
    let sm = FakeSMAppService(); sm.registerError = NSError(domain: "sm", code: 1)
    let gone = FlagBox()
    let runner = ScriptedRunner { _, args in
        if args == ["uninstall"] { gone.set() }
        return CommandResult(exitCode: 0, stdout: "", stderr: "")
    }
    let outcomes = await moveService(sm, runner, cliGone: gone).moveToEmber(.t3)
    #expect(runner.calls.map(\.0).last == "/Users/x/go/bin/ember-t3-producer")
    #expect(runner.calls.map(\.1).last == ["install"])
    guard case .moveFailed(_, _, let restored)? = outcomes.first?.error as? ProducerInstallError else {
        Issue.record("want moveFailed"); return
    }
    #expect(restored)
}

@MainActor @Test func aFailedTakeoverWithoutRestoreSaysReportingIsOff() async {
    let sm = FakeSMAppService(); sm.registerError = NSError(domain: "sm", code: 1)
    let gone = FlagBox()
    let runner = ScriptedRunner { _, args in
        if args == ["uninstall"] { gone.set() }
        return CommandResult(exitCode: args == ["install"] ? 1 : 0, stdout: "", stderr: "")
    }
    let outcomes = await moveService(sm, runner, cliGone: gone).moveToEmber(.t3)
    let error = outcomes.first?.error as? ProducerInstallError
    guard case .moveFailed(_, _, let restored)? = error else { Issue.record("want moveFailed"); return }
    #expect(!restored)
    #expect(error?.localizedDescription.contains("isn't reporting") == true)
    #expect(error?.localizedDescription.contains("ember-t3-producer install") == true)
}

@MainActor @Test func claudeCantMoveWhileSettingsAreUnreadable() async {
    let sm = FakeSMAppService()
    let runner = ScriptedRunner { _, _ in CommandResult(exitCode: 0, stdout: "", stderr: "") }
    let cliClaude = "/Users/x/Library/LaunchAgents/com.ember.heartbeat.plist"
    let svc = moveService(sm, runner, cliGone: FlagBox(),
                          extraFiles: [cliClaude: "x", "/Users/x/.claude/settings.json": "{,"])
    let outcomes = await svc.moveToEmber(.claude)
    #expect(outcomes.first?.error as? ProducerInstallError == .settingsUnreadable)
    #expect(runner.calls.isEmpty)
}

@MainActor @Test func masterOnWithOnlyCLIAgentsSaysSo() async {
    let sm = FakeSMAppService()
    let svc = service(sm, dirs: ["/Users/x/.t3"], files: [cliT3: "x"])
    let outcomes = await svc.installAll()
    #expect(outcomes.map(\.agent) == [.t3])
    #expect(outcomes.first?.error as? ProducerInstallError == .cliInstalled)
    #expect(sm.registered.isEmpty)
}
