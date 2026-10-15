import Testing
import Foundation
@testable import EmberKit

@MainActor
private final class RecordingExecutor: ControlsExecutor {
    var calls: [String] = []
    var granted = true
    var succeeds = true
    var busy = false

    func set(granted: Bool) { self.granted = granted }
    func set(succeeds: Bool) { self.succeeds = succeeds }
    func set(busy: Bool) { self.busy = busy }

    func changeVolume(by points: Int) -> Bool { record("volume \(points)") }
    func toggleMute() -> Bool { record("mute") }
    func press(_ key: MediaKey) -> Bool { record("media \(key.rawValue)") }
    func scroll(_ axis: ScrollAxis, lines: Int) -> Bool { record("scroll \(axis.rawValue) \(lines)") }
    func runShortcut(_ name: String) -> ScriptLaunch { launch("shortcut \(name)") }
    func runAppleScript(_ source: String) -> ScriptLaunch { launch("applescript \(source)") }
    func canPostEvents() async -> Bool { granted }

    private func launch(_ s: String) -> ScriptLaunch {
        if busy { return .busy }
        return record(s) ? .launched : .failed
    }

    private func record(_ s: String) -> Bool {
        calls.append(s)
        return succeeds
    }
}

private actor FakeControls: ControlsCommandSource {
    var rounds: [Result<[ControlCommand], Error>]
    var macs: [String] = []
    init(_ rounds: [Result<[ControlCommand], Error>]) { self.rounds = rounds }
    func commands(mac: String, wait: Int) async throws -> [ControlCommand] {
        macs.append(mac)
        if rounds.isEmpty {
            try await Task.sleep(for: .seconds(3600))
            return []
        }
        return try rounds.removeFirst().get()
    }
}

private func devicesGolden(_ name: String) throws -> Data {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("cmd/ember/testdata/devices/\(name).json")
    return try Data(contentsOf: url)
}

private let mapping = ControlsMapping(
    push: ControlAction(kind: .media, key: .previous), doublePush: ControlAction(kind: .shortcut, name: "Focus"),
    longPush: ControlAction(kind: .applescript, script: "beep"), turn: ControlAction(kind: .volume, step: 3))

private func decodeCommands(_ json: String) throws -> [ControlCommand] {
    try JSONDecoder().decode(ControlCommands.self, from: Data(json.utf8)).commands
}

@Test func controlCommandsDecodeGestureStepsAndAge() throws {
    let c = try decodeCommands(#"{"commands":[{"id":"k1","gesture":"turn","steps":-3,"age_ms":12},{"id":"k2","gesture":"long_push","age_ms":-4}]}"#)
    #expect(c == [ControlCommand(id: "k1", gesture: .turn, steps: -3, ageMs: 12), ControlCommand(id: "k2", gesture: .longPush)])
}

@Test(arguments: [
    #"{"commands":[{"id":"k1","gesture":"teleport"}]}"#,
    #"{"commands":[{"id":"k1","gesture":"push","script":"do shell script \"rm -rf ~\""}]}"#,
    #"{"commands":[{"id":"k1","gesture":"push","kind":"applescript"}]}"#,
    #"{"commands":[{"id":"k1","gesture":"push","name":"Evil"}]}"#,
    #"{"commands":[{"id":"k1","gesture":"push","steps":2}]}"#,
    #"{"commands":[{"id":"k1","gesture":"turn"}]}"#,
    #"{"commands":[{"id":"k1","gesture":"turn","steps":101}]}"#,
    #"{"commands":[{"id":"k1","gesture":"turn","steps":-9223372036854775808}]}"#,
    #"{"commands":[{"gesture":"push"}]}"#,
    #"{"commands":[{"id":"k1","gesture":"push"},{"id":"k2","gesture":"push","extra":1}]}"#,
    #"{"nothing":[]}"#,
])
func controlCommandsRejectUnknownGesturesAndExtraFields(_ json: String) {
    #expect(throws: (any Error).self) { try decodeCommands(json) }
}

@MainActor
@Test func aRejectedAnswerRunsNothingAndTheListenerKeepsPolling() async {
    let ex = RecordingExecutor()
    let client = stubbedClient(token: "t") { req in
        (okResponse(req.url!), Data(#"{"commands":[{"id":"k1","gesture":"push","script":"beep"}]}"#.utf8))
    }
    let source = ControlsClient(client: client)
    await #expect(throws: (any Error).self) { _ = try await source.commands(mac: "M4", wait: 0) }
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: { true },
                                    sleep: { _ in try Task.checkCancellation(); try await Task.sleep(for: .milliseconds(5)) })
    listener.start(source: source, mac: "M4")
    try? await Task.sleep(for: .milliseconds(50))
    listener.stop()
    await listener.join()
    #expect(await ex.calls.isEmpty)
}

@Test func controlsConfigCarriesOnlyTheTarget() throws {
    let cfg = try JSONDecoder().decode(ControlsConfig.self, from: Data(#"""
    {"target":"M4","macs":[{"name":"M4","listening":true,"last_poll":"2026-10-14T10:00:00Z"},{"name":"M5","listening":false}]}
    """#.utf8))
    #expect(cfg.target == "M4" && cfg.macs.map(\.name) == ["M4", "M5"])
    let sent = try JSONSerialization.jsonObject(with: JSONEncoder().encode(cfg)) as? [String: Any]
    #expect(sent?.keys.sorted() == ["target"])
}

@Test func controlsMappingDefaultsAndRoundTripsThroughDefaults() throws {
    let defaults = try #require(UserDefaults(suiteName: "controls-\(UUID().uuidString)"))
    #expect(ControlsMapping.load(from: defaults) == ControlsMapping())
    mapping.save(to: defaults)
    #expect(ControlsMapping.load(from: defaults) == mapping)
    #expect(ControlsMapping().needsEventAccess && mapping.needsEventAccess)
}

@Test func controlsTargetChoicesIncludeThisMacAndTheSavedTarget() {
    let cfg = ControlsConfig(target: "Old Mac", macs: [ControlsMac(name: "M5", listening: true)])
    #expect(cfg.targetChoices(thisMac: "M4") == ["M4", "M5", "Old Mac"])
}

@Test func controlsGateFailsClosed() {
    #expect(ControlsGate.shouldListen(target: "M4", thisMac: "M4", hasToken: true))
    #expect(!ControlsGate.shouldListen(target: nil, thisMac: "M4", hasToken: true))
    #expect(!ControlsGate.shouldListen(target: "", thisMac: "M4", hasToken: true))
    #expect(!ControlsGate.shouldListen(target: "M5", thisMac: "M4", hasToken: true))
    #expect(!ControlsGate.shouldListen(target: "M4", thisMac: "M4", hasToken: false))
}

@Test func switchingAnActionKindKeepsOnlyItsFields() {
    let a = ControlAction(kind: .shortcut, name: "Focus").switched(to: .scroll)
    #expect(a == ControlAction(kind: .scroll, lines: 3, axis: .vertical))
    #expect(a.switched(to: .media) == ControlAction(kind: .media, key: .playPause))
}

@Test func thisMacPrefersTheSourceAndClipsTo64() {
    #expect(ThisMac.name(source: "  m4 ", computerName: "Dmitry's MacBook") == "m4")
    #expect(ThisMac.name(source: "", computerName: "Dmitry's MacBook") == "Dmitry's MacBook")
    #expect(ThisMac.name(source: String(repeating: "x", count: 80), computerName: "").count == 64)
}

@Test func dispatcherRunsTheLocallyMappedAction() async {
    let ex = RecordingExecutor()
    for c in [ControlCommand(id: "1", gesture: .push), ControlCommand(id: "2", gesture: .doublePush),
              ControlCommand(id: "3", gesture: .longPush), ControlCommand(id: "4", gesture: .turn, steps: -2)] {
        #expect(await ControlDispatcher.perform(c, mapping: { mapping }, on: ex, isTarget: { true }) == .ran)
    }
    var scroll = mapping
    scroll.turn = ControlAction(kind: .scroll, lines: 4, axis: .horizontal)
    scroll.push = ControlAction(kind: .mute)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "5", gesture: .turn, steps: 2), mapping: { scroll }, on: ex, isTarget: { true }) == .ran)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "6", gesture: .push), mapping: { scroll }, on: ex, isTarget: { true }) == .ran)
    #expect(await ex.calls == ["media previous", "shortcut Focus", "applescript beep", "volume -6", "scroll horizontal 8", "mute"])
}

@Test func dispatcherRunsNothingForUnassignedOrMismatchedSlots() async {
    let ex = RecordingExecutor()
    let bad = ControlsMapping(push: ControlAction(kind: .volume, step: 2), doublePush: ControlAction(),
                              longPush: ControlAction(kind: .shortcut, name: "  "), turn: ControlAction(kind: .applescript, script: "beep"))
    for c in [ControlCommand(id: "1", gesture: .push), ControlCommand(id: "2", gesture: .doublePush),
              ControlCommand(id: "3", gesture: .longPush), ControlCommand(id: "4", gesture: .turn, steps: 1)] {
        #expect(await ControlDispatcher.perform(c, mapping: { bad }, on: ex, isTarget: { true }) == .unassigned)
    }
    #expect(await ex.calls.isEmpty)
}

@Test func dispatcherNeedsAccessibilityOnlyForEventKinds() async {
    let ex = RecordingExecutor()
    await ex.set(granted: false)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "1", gesture: .push), mapping: { mapping }, on: ex, isTarget: { true }) == .needsAccessibility)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "2", gesture: .turn, steps: 1), mapping: { mapping }, on: ex, isTarget: { true }) == .ran)
    #expect(await ex.calls == ["volume 3"])
}

@Test func dispatcherDropsStaleTurnsSoonerThanPresses() async {
    let ex = RecordingExecutor()
    #expect(await ControlDispatcher.perform(ControlCommand(id: "1", gesture: .turn, steps: 1), mapping: { mapping }, on: ex,
                                            age: .milliseconds(2_001), isTarget: { true }) == .stale)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "2", gesture: .doublePush), mapping: { mapping }, on: ex,
                                            age: .milliseconds(2_001), isTarget: { true }) == .ran)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "3", gesture: .doublePush), mapping: { mapping }, on: ex,
                                            age: .milliseconds(5_001), isTarget: { true }) == .stale)
    await ex.set(succeeds: false)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "4", gesture: .doublePush), mapping: { mapping }, on: ex, isTarget: { true }) == .failed)
    #expect(await ex.calls == ["shortcut Focus", "shortcut Focus"])
}

@MainActor
@Test func controlsListenerPollsAsThisMacAndRunsCommandsInOrder() async {
    let ex = RecordingExecutor()
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: { true },
                                    sleep: { _ in try Task.checkCancellation() })
    let source = FakeControls([.success([
        ControlCommand(id: "1", gesture: .push), ControlCommand(id: "2", gesture: .turn, steps: 2),
    ])])
    listener.start(source: source, mac: "M4")
    for _ in 0..<500 where await source.macs.count < 2 { try? await Task.sleep(for: .milliseconds(2)) }
    #expect(await ex.calls == ["media previous", "volume 6"])
    #expect(await source.macs.allSatisfy { $0 == "M4" })
    listener.stop()
    await listener.join()
}

@MainActor
@Test func controlsListenerRunsNothingWhenThisMacIsNotTheTarget() async {
    let ex = RecordingExecutor()
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: { false })
    let out = await listener.execute([ControlCommand(id: "1", gesture: .longPush)])
    #expect(out == [.notTarget])
    #expect(await ex.calls.isEmpty)
}

@MainActor
@Test func controlsListenerCountsLocalDelayTowardsAge() async {
    let ex = RecordingExecutor()
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: { true })
    let out = await listener.execute([ControlCommand(id: "1", gesture: .turn, steps: 1, ageMs: 500)],
                                     received: .now - .seconds(2))
    #expect(out == [.stale])
    #expect(await ex.calls.isEmpty)
}

@Test func controlsClientDecodesTheLongPoll() async throws {
    let seen = LockedBox()
    let client = stubbedClient { req in
        seen.add(req.url!.absoluteString)
        return (okResponse(req.url!), Data(#"{"commands":[{"id":"k1","gesture":"push","age_ms":3}]}"#.utf8))
    }
    let got = try await ControlsClient(client: client).commands(mac: "Dmitry's Mac", wait: 25)
    #expect(got == [ControlCommand(id: "k1", gesture: .push, ageMs: 3)])
    #expect(seen.paths.first?.contains("/v1/controls/commands?mac=Dmitry's%20Mac&wait=25") == true)
}

@Test func controlsActionGoldenDecodes() throws {
    let obj = try JSONSerialization.jsonObject(with: try devicesGolden("controls_action")) as? [String: Any]
    #expect(obj?["status"] as? String == "queued")
    #expect(obj?.keys.sorted() == ["status", "target"])
}

@MainActor
private final class GatedExecutor: ControlsExecutor {
    var calls: [String] = []
    var observe: (() -> Void)?
    private let gateOn: Int
    private var checks = 0
    private var waiting: CheckedContinuation<Void, Never>?
    private var answer: CheckedContinuation<Bool, Never>?

    init(gateOn: Int = 1) { self.gateOn = gateOn }

    func waitGated() async {
        if answer != nil { return }
        await withCheckedContinuation { waiting = $0 }
    }
    func grant() { answer?.resume(returning: true); answer = nil }

    func canPostEvents() async -> Bool {
        checks += 1
        guard checks == gateOn else { return true }
        return await withCheckedContinuation { c in
            answer = c
            waiting?.resume(); waiting = nil
        }
    }
    func press(_ key: MediaKey) -> Bool { effect("media \(key.rawValue)") }
    func changeVolume(by points: Int) -> Bool { effect("volume \(points)") }
    func toggleMute() -> Bool { effect("mute") }
    func scroll(_ axis: ScrollAxis, lines: Int) -> Bool { effect("scroll \(lines)") }
    func runShortcut(_ name: String) -> ScriptLaunch { effect("shortcut \(name)") ? .launched : .failed }
    func runAppleScript(_ source: String) -> ScriptLaunch { effect("applescript \(source)") ? .launched : .failed }

    private func effect(_ s: String) -> Bool {
        observe?()
        calls.append(s)
        return true
    }
}

private final class SignalLog: @unchecked Sendable {
    private let lock = NSLock()
    private var sent: [(pid_t, Int32)] = []

    func send(_ pid: pid_t, _ sig: Int32) -> Int32 {
        lock.withLock { sent.append((pid, sig)) }
        return kill(pid, sig)
    }
    func signals(_ sig: Int32) -> [pid_t] { lock.withLock { sent.filter { $0.1 == sig }.map(\.0) } }
}

private func scratchFile(_ ext: String) -> URL {
    FileManager.default.temporaryDirectory.appendingPathComponent("ember-test-\(UUID().uuidString).\(ext)")
}

private func readPid(_ url: URL) async throws -> pid_t {
    for _ in 0..<500 {
        if let text = try? String(contentsOf: url, encoding: .utf8),
           let pid = pid_t(text.trimmingCharacters(in: .whitespacesAndNewlines)) { return pid }
        try await Task.sleep(for: .milliseconds(10))
    }
    return 0
}

private func alive(_ pid: pid_t) -> Bool { pid > 0 && kill(pid, 0) == 0 }

@MainActor
@Test func controlsListenerStopsBetweenActionsWhenTheTargetMoves() async {
    let ex = GatedExecutor(gateOn: 2)
    var target = true
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: { target })
    let run = Task { await listener.execute([ControlCommand(id: "1", gesture: .push), ControlCommand(id: "2", gesture: .push)]) }
    await ex.waitGated()
    target = false
    ex.grant()
    #expect(await run.value == [.ran, .notTarget])
    #expect(ex.calls == ["media previous"])
}

@MainActor
@Test func controlsListenerStopsBetweenActionsWhenStopped() async {
    let ex = GatedExecutor(gateOn: 2)
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: { true },
                                    sleep: { _ in try Task.checkCancellation() })
    let source = FakeControls([.success([ControlCommand(id: "1", gesture: .push), ControlCommand(id: "2", gesture: .push)])])
    listener.start(source: source, mac: "M4")
    await ex.waitGated()
    listener.stop()
    ex.grant()
    for _ in 0..<50 where ex.calls.count < 2 { try? await Task.sleep(for: .milliseconds(4)) }
    #expect(ex.calls == ["media previous"])
}

@MainActor
@Test func controlsListenerRechecksTheTargetAfterTheAccessibilityCheck() async {
    let ex = GatedExecutor()
    var target = true
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: { target })
    let run = Task { await listener.execute([ControlCommand(id: "1", gesture: .push)]) }
    await ex.waitGated()
    target = false
    ex.grant()
    #expect(await run.value == [.notTarget])
    #expect(ex.calls.isEmpty)
}

@MainActor
@Test func controlsListenerRunsNothingWhenTheSlotChangesDuringTheAccessibilityCheck() async {
    let ex = GatedExecutor()
    var current = mapping
    let listener = ControlsListener(executor: ex, mapping: { current }, isTarget: { true })
    let run = Task { await listener.execute([ControlCommand(id: "1", gesture: .push)]) }
    await ex.waitGated()
    current.push = ControlAction(kind: .applescript, script: "beep")
    ex.grant()
    #expect(await run.value == [.stale])
    #expect(ex.calls.isEmpty)
}

@MainActor
@Test func theEffectRunsBeforeAnythingElseCanMoveTheTarget() async {
    let ex = GatedExecutor()
    var target = true
    var seen: [Bool] = []
    ex.observe = { seen.append(target) }
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: {
        Task { @MainActor in target = false }
        return target
    })
    for c in [ControlCommand(id: "1", gesture: .turn, steps: 1), ControlCommand(id: "2", gesture: .doublePush)] {
        target = true
        #expect(await listener.execute([c]) == [.ran])
        await Task.yield()
    }
    #expect(seen == [true, true])
    #expect(ex.calls == ["volume 3", "shortcut Focus"])
}

@MainActor
@Test func aPressDroppedByABusyRunnerIsADropNotAFailure() async {
    let ex = RecordingExecutor()
    ex.set(busy: true)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "1", gesture: .doublePush), mapping: { mapping }, on: ex,
                                            isTarget: { true }) == .busy)
    let listener = ControlsListener(executor: ex, mapping: { mapping }, isTarget: { true })
    #expect(await listener.execute([ControlCommand(id: "2", gesture: .longPush)]) == [.busy])
    #expect(ex.calls.isEmpty)
}

@Test func scriptRunnerRunsOneProcessAtATime() async throws {
    let runner = ScriptProcessRunner(timeout: .seconds(30))
    let release = scratchFile("release")
    defer { try? FileManager.default.removeItem(at: release) }
    let hold = ["-c", "while [ ! -e \"$0\" ]; do /bin/sleep 0.02; done", release.path]
    #expect(runner.run("/bin/sh", hold, what: "test") == .launched)
    #expect(runner.run("/bin/sh", hold, what: "test") == .busy)
    #expect(runner.isBusy)
    FileManager.default.createFile(atPath: release.path, contents: nil)
    for _ in 0..<500 where runner.isBusy { try await Task.sleep(for: .milliseconds(10)) }
    #expect(!runner.isBusy)
}

@Test func scriptRunnerKillsAProcessPastItsTimeout() async throws {
    let runner = ScriptProcessRunner(timeout: .milliseconds(200))
    let started = ContinuousClock.now
    #expect(runner.run("/bin/sleep", ["30"], what: "test") == .launched)
    for _ in 0..<300 where runner.isBusy { try await Task.sleep(for: .milliseconds(10)) }
    #expect(!runner.isBusy)
    #expect(ContinuousClock.now - started < .seconds(3))
    #expect(runner.run("/usr/bin/true", [], what: "test") == .launched)
}

@Test func scriptRunnerTerminatesTheWholeProcessGroupPastItsTimeout() async throws {
    let runner = ScriptProcessRunner(timeout: .seconds(1))
    let pidFile = scratchFile("pid")
    defer { try? FileManager.default.removeItem(at: pidFile) }
    let started = ContinuousClock.now
    #expect(runner.run("/bin/sh", ["-c", "/bin/sleep 30 & echo $! > \"$0\"; wait", pidFile.path], what: "test") == .launched)
    let grandchild = try await readPid(pidFile)
    #expect(grandchild > 0)
    for _ in 0..<500 where alive(grandchild) { try await Task.sleep(for: .milliseconds(10)) }
    #expect(grandchild > 0 && !alive(grandchild))
    #expect(ContinuousClock.now - started < .seconds(1) + ScriptProcessRunner.killGrace * 0.9)
    if alive(grandchild) { kill(grandchild, SIGKILL) }
}

@Test func scriptRunnerKillsTheWholeProcessGroupThatIgnoresTerm() async throws {
    let runner = ScriptProcessRunner(timeout: .seconds(1))
    let pidFile = scratchFile("pid")
    defer { try? FileManager.default.removeItem(at: pidFile) }
    #expect(runner.run("/bin/sh", ["-c", "trap '' TERM; /bin/sleep 30 & echo $! > \"$0\"; wait", pidFile.path],
                       what: "test") == .launched)
    let grandchild = try await readPid(pidFile)
    #expect(grandchild > 0)
    for _ in 0..<500 where alive(grandchild) { try await Task.sleep(for: .milliseconds(10)) }
    #expect(grandchild > 0 && !alive(grandchild))
    if alive(grandchild) { kill(grandchild, SIGKILL) }
    for _ in 0..<300 where runner.isBusy { try await Task.sleep(for: .milliseconds(10)) }
    #expect(!runner.isBusy)
}

@Test func scriptRunnerSkipsTheKillOnceTheGroupIsGone() async throws {
    let log = SignalLog()
    let runner = ScriptProcessRunner(timeout: .milliseconds(300), signal: { log.send($0, $1) })
    #expect(runner.run("/bin/sleep", ["30"], what: "test") == .launched)
    for _ in 0..<300 where runner.isBusy { try await Task.sleep(for: .milliseconds(10)) }
    #expect(!runner.isBusy)
    try await Task.sleep(for: ScriptProcessRunner.killGrace + .milliseconds(300))
    #expect(log.signals(SIGTERM).count == 1)
    #expect(log.signals(SIGTERM).allSatisfy { $0 < 0 })
    #expect(log.signals(SIGKILL).isEmpty)
}

@Test func scriptRunnerSurvivesAProcessThatExitsBeforeReadingStdin() async throws {
    let runner = ScriptProcessRunner()
    let big = Data(repeating: 0x41, count: 4 << 20)
    for _ in 0..<3 {
        #expect(runner.run("/usr/bin/true", [], stdin: big, what: "test") == .launched)
        for _ in 0..<300 where runner.isBusy { try await Task.sleep(for: .milliseconds(10)) }
    }
    try await Task.sleep(for: .milliseconds(300))
    #expect(!runner.isBusy)
}

@Test func scriptRunnerReportsAToolThatCannotStart() {
    let runner = ScriptProcessRunner()
    #expect(runner.run("/nonexistent/tool", [], what: "test") == .failed)
    #expect(!runner.isBusy)
}

@Test func scriptRunnerStopEndsTheRunningProcessGroup() async throws {
    let log = SignalLog()
    let runner = ScriptProcessRunner(timeout: .seconds(30), signal: { log.send($0, $1) })
    let pidFile = scratchFile("pid")
    defer { try? FileManager.default.removeItem(at: pidFile) }
    #expect(runner.run("/bin/sh", ["-c", "/bin/sleep 30 & echo $! > \"$0\"; wait", pidFile.path], what: "test") == .launched)
    let grandchild = try await readPid(pidFile)
    #expect(grandchild > 0)
    let started = ContinuousClock.now
    runner.stop()
    #expect(ContinuousClock.now - started < .seconds(1))
    for _ in 0..<200 where alive(grandchild) { try await Task.sleep(for: .milliseconds(10)) }
    #expect(grandchild > 0 && !alive(grandchild))
    #expect(log.signals(SIGTERM).count == 1 && log.signals(SIGTERM).allSatisfy { $0 < 0 })
    #expect(log.signals(SIGKILL).isEmpty)
    if alive(grandchild) { kill(grandchild, SIGKILL) }
}

@Test func scriptRunnerStopKillsAGroupThatIgnoresTerm() async throws {
    let log = SignalLog()
    let runner = ScriptProcessRunner(timeout: .seconds(30), signal: { log.send($0, $1) })
    let pidFile = scratchFile("pid")
    defer { try? FileManager.default.removeItem(at: pidFile) }
    #expect(runner.run("/bin/sh", ["-c", "trap '' TERM; /bin/sleep 30 & echo $! > \"$0\"; wait", pidFile.path],
                       what: "test") == .launched)
    let grandchild = try await readPid(pidFile)
    #expect(grandchild > 0)
    let started = ContinuousClock.now
    runner.stop(grace: .milliseconds(200))
    #expect(ContinuousClock.now - started < .seconds(1))
    for _ in 0..<200 where alive(grandchild) { try await Task.sleep(for: .milliseconds(10)) }
    #expect(grandchild > 0 && !alive(grandchild))
    #expect(log.signals(SIGKILL).count == 1 && log.signals(SIGKILL).allSatisfy { $0 < 0 })
    if alive(grandchild) { kill(grandchild, SIGKILL) }
}

@Test func scriptRunnerRefusesScriptsOnceStopped() {
    let log = SignalLog()
    let runner = ScriptProcessRunner(timeout: .seconds(30), signal: { log.send($0, $1) })
    runner.stop()
    #expect(runner.run("/bin/sleep", ["30"], what: "test") == .failed)
    #expect(!runner.isBusy)
    #expect(log.signals(SIGTERM).isEmpty && log.signals(SIGKILL).isEmpty)
}
