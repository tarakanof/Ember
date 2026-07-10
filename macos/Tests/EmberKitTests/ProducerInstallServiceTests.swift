import Testing
import Foundation
@testable import EmberKit

@MainActor final class FakeSMAppService: @preconcurrency SMAppServiceControlling {
    var statuses: [String: AgentRegistration] = [:]
    var registered: [String] = []; var unregistered: [String] = []
    var registerError: Error?
    func register(plistName: String) throws { if let e = registerError { throw e }; registered.append(plistName); statuses[plistName] = .enabled }
    func unregister(plistName: String) throws { unregistered.append(plistName); statuses[plistName] = .notRegistered }
    func status(plistName: String) -> AgentRegistration { statuses[plistName] ?? .notRegistered }
}
@MainActor final class FakeRunner: @preconcurrency ProducerCommandRunning {
    var calls: [(String, [String])] = []
    var exitFor: ([String]) -> Int32 = { _ in 0 }
    func run(executable: String, arguments: [String]) throws -> CommandResult {
        calls.append((executable, arguments)); return CommandResult(exitCode: exitFor(arguments), stdout: "", stderr: "")
    }
}

@MainActor @Test func detectsOnlyPresentAgents() {
    let home = URL(fileURLWithPath: "/Users/x")
    let svc = ProducerInstallService(sm: FakeSMAppService(), runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: home,
        fileExists: { $0.hasSuffix("/.claude") })   // only claude present
    #expect(svc.detectedAgents() == [.claude])
}

@MainActor @Test func installRunsConfigureThenRegister() throws {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    let svc = ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    try svc.install(.claude)
    #expect(runner.calls.first?.1 == ["configure"])
    #expect(runner.calls.first?.0.hasSuffix("ember-claude-producer") == true)
    #expect(sm.registered == ["com.ember.heartbeat.plist"])
}

@MainActor @Test func registerFailureRollsBackConfigure() {
    let sm = FakeSMAppService(); sm.registerError = NSError(domain: "x", code: 1)
    let runner = FakeRunner()
    let svc = ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    #expect(throws: (any Error).self) { try svc.install(.claude) }
    #expect(runner.calls.map(\.1) == [["configure"], ["deconfigure"]])   // configure then rollback
    #expect(sm.registered.isEmpty)
}

@MainActor @Test func installThrowsOnConfigureNonZeroExit() {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    runner.exitFor = { _ in 1 }
    let svc = ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    #expect(throws: (any Error).self) { try svc.install(.claude) }
    #expect(runner.calls.count == 1)   // no rollback attempt since configure itself failed
    #expect(sm.registered.isEmpty)
}

@MainActor @Test func uninstallRunsUnregisterThenDeconfigure() throws {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    let svc = ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    try svc.uninstall(.codex)
    #expect(sm.unregistered == ["com.ember.codex.plist"])
    #expect(runner.calls.first?.1 == ["deconfigure"])
    #expect(runner.calls.first?.0.hasSuffix("ember-codex-producer") == true)
}
