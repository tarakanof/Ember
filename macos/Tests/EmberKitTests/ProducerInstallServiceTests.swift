import Testing
import Foundation
@testable import EmberKit

/// Fakes are called off the MainActor by the `@concurrent` batch operations,
/// so their state is lock-guarded rather than actor-isolated.
final class FakeSMAppService: SMAppServiceControlling, @unchecked Sendable {
    private let lock = NSLock()
    private var _statuses: [String: AgentRegistration] = [:]
    private var _registered: [String] = []
    private var _unregistered: [String] = []
    private var _registerError: Error?
    var statuses: [String: AgentRegistration] {
        get { lock.withLock { _statuses } } set { lock.withLock { _statuses = newValue } }
    }
    var registered: [String] { lock.withLock { _registered } }
    var unregistered: [String] { lock.withLock { _unregistered } }
    var registerError: Error? {
        get { lock.withLock { _registerError } } set { lock.withLock { _registerError = newValue } }
    }
    func register(plistName: String) throws {
        try lock.withLock {
            if let e = _registerError { throw e }
            _registered.append(plistName); _statuses[plistName] = .enabled
        }
    }
    private var _unregisterError: Error?
    var unregisterError: Error? {
        get { lock.withLock { _unregisterError } } set { lock.withLock { _unregisterError = newValue } }
    }
    func unregister(plistName: String) throws {
        try lock.withLock {
            if let e = _unregisterError { throw e }
            _unregistered.append(plistName); _statuses[plistName] = .notRegistered
        }
    }
    func status(plistName: String) -> AgentRegistration { lock.withLock { _statuses[plistName] ?? .notRegistered } }
}
final class FakeRunner: ProducerCommandRunning, @unchecked Sendable {
    private let lock = NSLock()
    private var _calls: [(String, [String])] = []
    private var _ranOnMainThread: [Bool] = []
    private var _exitFor: @Sendable ([String]) -> Int32 = { _ in 0 }
    var calls: [(String, [String])] { lock.withLock { _calls } }
    var ranOnMainThread: [Bool] { lock.withLock { _ranOnMainThread } }
    var exitFor: @Sendable ([String]) -> Int32 {
        get { lock.withLock { _exitFor } } set { lock.withLock { _exitFor = newValue } }
    }
    func run(executable: String, arguments: [String]) throws -> CommandResult {
        let exit = lock.withLock {
            _calls.append((executable, arguments))
            _ranOnMainThread.append(Thread.isMainThread)
            return _exitFor(arguments)
        }
        return CommandResult(exitCode: exit, stdout: "", stderr: "")
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
