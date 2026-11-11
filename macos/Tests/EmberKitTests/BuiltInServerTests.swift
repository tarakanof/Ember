import Testing
import Foundation
@testable import EmberKit

final class FakeServerProbe: BuiltInServerProbing, @unchecked Sendable {
    private let lock = NSLock()
    private var _healths: [ServerHealth] = []
    private var _fallback: ServerHealth = .closed
    var version: String? = "v0.90.0"

    func script(_ healths: [ServerHealth], then fallback: ServerHealth) {
        lock.withLock { _healths = healths; _fallback = fallback }
    }

    func health() async -> ServerHealth {
        lock.withLock { _healths.isEmpty ? _fallback : _healths.removeFirst() }
    }

    func version() async -> String? { version }
}

final class StatusLog: @unchecked Sendable {
    private let lock = NSLock()
    private var _entries: [BuiltInServerStatus] = []
    var entries: [BuiltInServerStatus] { lock.withLock { _entries } }
    func append(_ s: BuiltInServerStatus) { lock.withLock { _entries.append(s) } }
}

final class MintCounter: @unchecked Sendable {
    private let lock = NSLock()
    private var _count = 0
    var count: Int { lock.withLock { _count } }
    func mint() -> String {
        lock.withLock { _count += 1; return String(repeating: "ab", count: 32) }
    }
}

private let runningPrint = "gui/501/com.ember.server = {\n\tstate = running\n\tpid = 4242\n}\n"
private let waitingPrint = "gui/501/com.ember.server = {\n\tstate = not running\n\tlast exit code = 1\n}\n"

private struct Rig {
    let home: URL
    let sm = FakeSMAppService()
    let runner = FakeRunner()
    let probe = FakeServerProbe()
    let mint = MintCounter()
    let service: BuiltInServerService
    let paths: BuiltInServerPaths

    init(startAttempts: Int = 5) throws {
        home = FileManager.default.temporaryDirectory
            .appendingPathComponent("ember-builtin-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: home, withIntermediateDirectories: true,
                                                attributes: [.posixPermissions: 0o700])
        paths = BuiltInServerPaths(home: home)
        let jobs = ProducerInstallService(sm: sm, runner: runner,
            bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: home,
            fileExists: { _ in true }, uid: 501)
        let mint = self.mint
        service = BuiltInServerService(jobs: jobs, probe: probe, paths: paths,
            producerEnv: EnvFileStore(path: paths.producerEnv),
            mintToken: { mint.mint() }, pause: {}, startAttempts: startAttempts, stopAttempts: 3)
        jobPrint(runningPrint)
    }

    func jobPrint(_ text: String) {
        runner.stdoutFor = { $0.first == "print" ? text : "" }
    }

    func read(_ url: URL) -> EnvFile {
        EnvFile(parsing: (try? String(contentsOf: url, encoding: .utf8)) ?? "")
    }

    func writeProducerEnv(_ text: String) throws {
        try EnvFile(parsing: text).write(to: paths.producerEnv)
    }

    func cleanup() { try? FileManager.default.removeItem(at: home) }
}

@Test func statusRules() {
    #expect(builtInServerStatus(registration: .notRegistered, job: .running, health: .ok, version: "v") == .off)
    #expect(builtInServerStatus(registration: .requiresApproval, job: .notLoaded, health: .closed, version: nil) == .needsApproval)
    #expect(builtInServerStatus(registration: .notFound, job: .notLoaded, health: .closed, version: nil) == .missing)
    #expect(builtInServerStatus(registration: .enabled, job: .running, health: .ok, version: "v") == .running(version: "v"))
    #expect(builtInServerStatus(registration: .enabled, job: .running, health: .closed, version: nil) == .starting)
    #expect(builtInServerStatus(registration: .enabled, job: .waiting, health: .ok, version: nil) == .portInUse)
    #expect(builtInServerStatus(registration: .enabled, job: .waiting, health: .occupied, version: nil) == .portInUse)
    #expect(builtInServerStatus(registration: .enabled, job: .waiting, health: .closed, version: nil) == .notRunning)
    #expect(builtInServerStatus(registration: .enabled, job: .stuck, health: .closed, version: nil) == .notRunning)
}

@Test func jobStateReadsLaunchctlPrint() {
    #expect(serverJobState(CommandResult(exitCode: 0, stdout: runningPrint, stderr: "")) == .running)
    #expect(serverJobState(CommandResult(exitCode: 0, stdout: waitingPrint, stderr: "")) == .waiting)
    #expect(serverJobState(CommandResult(exitCode: 113, stdout: "", stderr: "Could not find service")) == .notLoaded)
}

@Test func mintedTokenIs32RandomBytesInHex() throws {
    let a = try mintServerToken()
    let b = try mintServerToken()
    #expect(a.count == 64)
    #expect(a.allSatisfy { $0.isHexDigit })
    #expect(a != b)
}

@Test func turningOnGoesFromStartingToRunning() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.probe.script([.closed, .closed], then: .ok)
    let log = StatusLog()

    let final = try await rig.service.turnOn { log.append($0) }

    #expect(log.entries == [.starting])
    #expect(final == .running(version: "v0.90.0"))
    #expect(rig.sm.registered == ["com.ember.server.plist"])
    #expect(FileManager.default.fileExists(atPath: rig.paths.dataDir.path))
    let token = rig.read(rig.paths.serverEnv).get(SettingsKeys.token)
    #expect(token.count == 64)
    let producer = rig.read(rig.paths.producerEnv)
    #expect(producer.get(SettingsKeys.serverURL) == "http://127.0.0.1:3627")
    #expect(producer.get(SettingsKeys.token) == token)
}

@Test func serverEnvIsPrivate() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.probe.script([.closed], then: .ok)
    _ = try await rig.service.turnOn()
    let attrs = try FileManager.default.attributesOfItem(atPath: rig.paths.serverEnv.path)
    #expect((attrs[.posixPermissions] as? NSNumber)?.int16Value == 0o600)
    let dataAttrs = try FileManager.default.attributesOfItem(atPath: rig.paths.dataDir.path)
    #expect((dataAttrs[.posixPermissions] as? NSNumber)?.int16Value == 0o700)
}

@Test func healthTimeoutEndsNotRunning() async throws {
    let rig = try Rig(startAttempts: 4); defer { rig.cleanup() }
    try rig.writeProducerEnv("EMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    rig.probe.script([], then: .closed)
    let final = try await rig.service.turnOn()
    #expect(final == .notRunning)
    #expect(rig.service.mode() == .thisMac)
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.serverURL) == "http://nas.test:3627")
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.token) == "remote")
}

@Test func aTakenPortStopsTurnOnBeforeAnythingChanges() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    try rig.writeProducerEnv("EMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    rig.probe.script([], then: .ok)
    await #expect(throws: BuiltInServerError.portInUse) { try await rig.service.turnOn() }
    #expect(rig.sm.registered.isEmpty)
    #expect(!FileManager.default.fileExists(atPath: rig.paths.serverEnv.path))
    let env = rig.read(rig.paths.producerEnv)
    #expect(env.get(SettingsKeys.serverURL) == "http://nas.test:3627")
    #expect(env.get(SettingsKeys.token) == "remote")
}

@Test func aPortTakenDuringStartRollsBackTheSwitch() async throws {
    let rig = try Rig(startAttempts: 3); defer { rig.cleanup() }
    try rig.writeProducerEnv("EMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    rig.jobPrint(waitingPrint)
    rig.probe.script([.closed], then: .ok)
    await #expect(throws: BuiltInServerError.portInUse) { try await rig.service.turnOn() }
    #expect(rig.service.mode() == .anotherServer)
    #expect(rig.sm.unregistered == ["com.ember.server.plist"])
    let env = rig.read(rig.paths.producerEnv)
    #expect(env.get(SettingsKeys.serverURL) == "http://nas.test:3627")
    #expect(env.get(SettingsKeys.token) == "remote")
}

@Test func steadyStatePortInUseIsReported() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.sm.statuses = ["com.ember.server.plist": .enabled]
    rig.jobPrint(waitingPrint)
    rig.probe.script([], then: .occupied)
    #expect(await rig.service.status() == .portInUse)
}

@Test func needsApprovalStopsWaiting() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.sm.statuses = ["com.ember.server.plist": .requiresApproval]
    final class ApprovalSM: SMAppServiceControlling, @unchecked Sendable {
        let inner: FakeSMAppService
        init(_ inner: FakeSMAppService) { self.inner = inner }
        func register(plistName: String) throws {
            try inner.register(plistName: plistName)
            inner.statuses[plistName] = .requiresApproval
        }
        func unregister(plistName: String) throws { try inner.unregister(plistName: plistName) }
        func status(plistName: String) -> AgentRegistration { inner.status(plistName: plistName) }
    }
    let jobs = ProducerInstallService(sm: ApprovalSM(rig.sm), runner: rig.runner,
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: rig.home,
        fileExists: { _ in true }, uid: 501)
    let service = BuiltInServerService(jobs: jobs, probe: rig.probe, paths: rig.paths,
        producerEnv: EnvFileStore(path: rig.paths.producerEnv), pause: {}, startAttempts: 30)
    rig.probe.script([], then: .closed)

    #expect(try await service.turnOn() == .needsApproval)
    #expect(service.mode() == .thisMac)
}

@Test func tokenIsMintedOnceAndReused() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.probe.script([.closed], then: .ok)
    _ = try await rig.service.turnOn()
    let first = rig.read(rig.paths.serverEnv).get(SettingsKeys.token)
    rig.probe.script([], then: .closed)
    _ = try await rig.service.turnOff()
    rig.probe.script([.closed], then: .ok)
    _ = try await rig.service.turnOn()
    #expect(rig.mint.count == 1)
    #expect(rig.read(rig.paths.serverEnv).get(SettingsKeys.token) == first)
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.token) == first)
}

@Test func anExistingServerTokenIsKept() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    try EnvFile(parsing: "# mine\nEMBER_TOKEN=keepme\nEMBER_MDNS_ADVERTISE=0\n").write(to: rig.paths.serverEnv)
    rig.probe.script([.closed], then: .ok)
    _ = try await rig.service.turnOn()
    #expect(rig.mint.count == 0)
    #expect(try String(contentsOf: rig.paths.serverEnv, encoding: .utf8)
        == "# mine\nEMBER_TOKEN=keepme\nEMBER_MDNS_ADVERTISE=0\n")
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.token) == "keepme")
}

@Test func switchingBackRestoresThePreviousServer() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    try rig.writeProducerEnv("EMBER_SOURCE=mbp\nEMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    rig.probe.script([.closed], then: .ok)
    _ = try await rig.service.turnOn()
    #expect(try await !rig.service.adoptProducers())
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.previousServerURL) == "http://nas.test:3627")

    rig.probe.script([.ok], then: .closed)
    #expect(try await rig.service.turnOff() == .off)

    #expect(rig.sm.unregistered == ["com.ember.server.plist"])
    let env = rig.read(rig.paths.producerEnv)
    #expect(env.get(SettingsKeys.source) == "mbp")
    #expect(env.get(SettingsKeys.serverURL) == "http://nas.test:3627")
    #expect(env.get(SettingsKeys.token) == "remote")
    #expect(!env.contains(SettingsKeys.previousServerURL))
    #expect(!env.contains(SettingsKeys.previousToken))
}

@Test func switchingBackWithNoPreviousServerClearsTheLocalOne() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.probe.script([.closed], then: .ok)
    _ = try await rig.service.turnOn()
    rig.probe.script([], then: .closed)
    _ = try await rig.service.turnOff()
    let env = rig.read(rig.paths.producerEnv)
    #expect(!env.contains(SettingsKeys.serverURL))
    #expect(!env.contains(SettingsKeys.token))
}

@Test func restoreWithoutRememberedKeysLeavesAHandSetServerAlone() {
    var env = EnvFile(parsing: "EMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    restoreRemote(in: &env, serverToken: "local")
    #expect(env.get(SettingsKeys.serverURL) == "http://nas.test:3627")
    #expect(env.get(SettingsKeys.token) == "remote")
}

@Test func startHealsAStuckJob() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.sm.statuses = ["com.ember.server.plist": .enabled]
    rig.runner.stdoutFor = { args in
        args.first == "print"
            ? "gui/501/com.ember.server = {\n\tstate = not running\n\tjob state = spawn failed\n\tlast exit code = 78: EX_CONFIG\n}\n"
            : ""
    }
    rig.probe.script([], then: .closed)
    _ = try await rig.service.start()
    #expect(rig.runner.calls.contains { $0.1 == ["bootout", "gui/501/com.ember.server"] })
    #expect(rig.sm.unregistered == ["com.ember.server.plist"])
    #expect(rig.sm.registered == ["com.ember.server.plist"])
}

@Test func reconcileReRegistersTheServerAfterAnUpdate() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.sm.statuses = ["com.ember.server.plist": .enabled]
    let jobs = ProducerInstallService(sm: rig.sm, runner: rig.runner,
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: rig.home,
        fileExists: { _ in true }, uid: 501)
    let outcomes = await jobs.reconcile(bundleChanged: true)
    #expect(outcomes.map(\.job) == [.server])
    #expect(rig.sm.registered == ["com.ember.server.plist"])
    #expect(await jobs.repairAll().isEmpty)
}

@Test func fingerprintCoversTheServer() throws {
    let rig = try Rig(); defer { rig.cleanup() }
    let app = rig.home.appendingPathComponent("Ember.app")
    let macos = app.appendingPathComponent("Contents/MacOS")
    try FileManager.default.createDirectory(at: macos, withIntermediateDirectories: true)
    let server = macos.appendingPathComponent("ember-server")
    try Data("one".utf8).write(to: server)
    let before = bundleFingerprint(appURL: app, version: "1", build: "1")
    try Data("two".utf8).write(to: server)
    #expect(bundleFingerprint(appURL: app, version: "1", build: "1") != before)
}

@MainActor @Test func anExistingInstallIsNeverSwitched() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    try rig.writeProducerEnv("EMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    let model = BuiltInServerModel(service: rig.service)
    await model.refresh()
    #expect(model.mode == .anotherServer)
    #expect(model.status == .off)
    #expect(rig.sm.registered.isEmpty)
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.serverURL) == "http://nas.test:3627")
}

@MainActor @Test func modelSwitchesModesAndReportsTheEnvChange() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.probe.script([.closed], then: .ok)
    let model = BuiltInServerModel(service: rig.service)
    var changes = 0
    model.onProducerEnvChanged = { changes += 1 }
    await model.refresh()

    await model.select(.thisMac)
    #expect(model.mode == .thisMac)
    #expect(model.status == .running(version: "v0.90.0"))
    #expect(!model.isWorking)

    rig.probe.script([], then: .closed)
    await model.select(.anotherServer)
    #expect(model.mode == .anotherServer)
    #expect(model.status == .off)
    #expect(changes == 2)
}

@MainActor @Test func modelRevertsWhenRegistrationFails() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    rig.sm.registerError = NSError(domain: "SMAppServiceErrorDomain", code: 1,
                                   userInfo: [NSLocalizedDescriptionKey: "Operation not permitted"])
    let model = BuiltInServerModel(service: rig.service)
    await model.select(.thisMac)
    #expect(model.mode == .anotherServer)
    #expect(model.status == .off)
    #expect(model.error == "Operation not permitted")
    #expect(!rig.read(rig.paths.producerEnv).contains(SettingsKeys.serverURL))
}

final class Sequence833: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [String]
    private let last: String
    init(_ items: [String], then last: String) { self.items = items; self.last = last }
    func next() -> String { lock.withLock { items.isEmpty ? last : items.removeFirst() } }
}

@Test func oneHealthyPollIsNotEnoughToHandOverTheToken() async throws {
    let rig = try Rig(startAttempts: 4); defer { rig.cleanup() }
    try rig.writeProducerEnv("EMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    let prints = Sequence833([runningPrint], then: waitingPrint)
    rig.runner.stdoutFor = { $0.first == "print" ? prints.next() : "" }
    rig.probe.script([.closed, .ok], then: .closed)
    #expect(try await rig.service.turnOn() == .notRunning)
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.serverURL) == "http://nas.test:3627")
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.token) == "remote")
}

final class ThrowingApprovalSM: SMAppServiceControlling, @unchecked Sendable {
    let inner = FakeSMAppService()
    func register(plistName: String) throws {
        inner.statuses[plistName] = .requiresApproval
        throw NSError(domain: "SMAppServiceErrorDomain", code: 1,
                      userInfo: [NSLocalizedDescriptionKey: "Operation not permitted"])
    }
    func unregister(plistName: String) throws { try inner.unregister(plistName: plistName) }
    func status(plistName: String) -> AgentRegistration { inner.status(plistName: plistName) }
}

@MainActor @Test func approvalGrantedLaterPointsTheProducers() async throws {
    let rig = try Rig(); defer { rig.cleanup() }
    try rig.writeProducerEnv("EMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    let sm = ThrowingApprovalSM()
    let jobs = ProducerInstallService(sm: sm, runner: rig.runner,
        bundleMacOSDir: URL(fileURLWithPath: "/App/Contents/MacOS"), home: rig.home,
        fileExists: { _ in true }, uid: 501)
    let service = BuiltInServerService(jobs: jobs, probe: rig.probe, paths: rig.paths,
        producerEnv: EnvFileStore(path: rig.paths.producerEnv), pause: {}, startAttempts: 3)
    let model = BuiltInServerModel(service: service)
    var changes = 0
    model.onProducerEnvChanged = { changes += 1 }
    rig.probe.script([], then: .closed)

    await model.select(.thisMac)
    #expect(model.mode == .thisMac)
    #expect(model.status == .needsApproval)
    #expect(model.error == nil)
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.serverURL) == "http://nas.test:3627")

    sm.inner.statuses["com.ember.server.plist"] = .enabled
    rig.probe.script([], then: .ok)
    await model.refresh()
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.serverURL) == "http://nas.test:3627")
    await model.refresh()
    let env = rig.read(rig.paths.producerEnv)
    #expect(env.get(SettingsKeys.serverURL) == "http://127.0.0.1:3627")
    #expect(env.get(SettingsKeys.token) == rig.read(rig.paths.serverEnv).get(SettingsKeys.token))
    #expect(env.get(SettingsKeys.previousServerURL) == "http://nas.test:3627")
    #expect(changes == 2)
}

@Test func aStaleConnectionDraftDoesNotUndoTheSwitch() throws {
    let loaded = ConnectionSettings(source: "mbp", serverURL: "http://nas.test:3627", sourceColor: "")
    var env = EnvFile(parsing: "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=http://127.0.0.1:3627\nEMBER_TOKEN=local\n")
    var draft = loaded
    draft.source = "mac"
    try draft.applyChanged(since: loaded, to: &env)
    #expect(env.get(SettingsKeys.source) == "mac")
    #expect(env.get(SettingsKeys.serverURL) == "http://127.0.0.1:3627")
    #expect(env.get(SettingsKeys.token) == "local")
}

@Test func restartIfLoadedKickstartsACommandLineJob() async {
    let runner = FakeRunner()
    let svc = ProducerInstallService(sm: FakeSMAppService(), runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true }, uid: 501)
    #expect(await svc.restartIfLoaded(.codex))
    #expect(runner.calls.map(\.1) == [["print", "gui/501/com.ember.codex"],
                                      ["kickstart", "-k", "gui/501/com.ember.codex"]])
    let unloaded = FakeRunner(); unloaded.exitFor = { $0.first == "print" ? 113 : 0 }
    let off = ProducerInstallService(sm: FakeSMAppService(), runner: unloaded,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true }, uid: 501)
    #expect(await !off.restartIfLoaded(.codex))
    #expect(!unloaded.calls.contains { $0.1.first == "kickstart" })
}

@MainActor @Test func startThatBringsTheServerUpReportsTheEnvChange() async throws {
    let rig = try Rig(startAttempts: 2); defer { rig.cleanup() }
    try rig.writeProducerEnv("EMBER_SERVER_URL=http://nas.test:3627\nEMBER_TOKEN=remote\n")
    let model = BuiltInServerModel(service: rig.service)
    var changes = 0
    model.onProducerEnvChanged = { changes += 1 }
    rig.probe.script([], then: .closed)
    await model.select(.thisMac)
    #expect(model.status == .notRunning)
    #expect(changes == 1)

    rig.probe.script([], then: .ok)
    await model.start()
    #expect(model.status == .running(version: "v0.90.0"))
    #expect(rig.read(rig.paths.producerEnv).get(SettingsKeys.serverURL) == "http://127.0.0.1:3627")
    #expect(changes == 2)
}
