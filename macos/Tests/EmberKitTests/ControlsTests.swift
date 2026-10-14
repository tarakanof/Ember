import Testing
import Foundation
@testable import EmberKit

private actor RecordingExecutor: ControlsExecutor {
    var calls: [String] = []
    var granted = true
    var succeeds = true

    func set(granted: Bool) { self.granted = granted }
    func set(succeeds: Bool) { self.succeeds = succeeds }

    func changeVolume(by points: Int) async -> Bool { record("volume \(points)") }
    func toggleMute() async -> Bool { record("mute") }
    func press(_ key: MediaKey) async -> Bool { record("media \(key.rawValue)") }
    func scroll(_ axis: ScrollAxis, lines: Int) async -> Bool { record("scroll \(axis.rawValue) \(lines)") }
    func runShortcut(_ name: String) async -> Bool { record("shortcut \(name)") }
    func runAppleScript(_ source: String) async -> Bool { record("applescript \(source)") }
    func canPostEvents() async -> Bool { granted }

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
        #expect(await ControlDispatcher.perform(c, mapping: mapping, on: ex) == .ran)
    }
    var scroll = mapping
    scroll.turn = ControlAction(kind: .scroll, lines: 4, axis: .horizontal)
    scroll.push = ControlAction(kind: .mute)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "5", gesture: .turn, steps: 2), mapping: scroll, on: ex) == .ran)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "6", gesture: .push), mapping: scroll, on: ex) == .ran)
    #expect(await ex.calls == ["media previous", "shortcut Focus", "applescript beep", "volume -6", "scroll horizontal 8", "mute"])
}

@Test func dispatcherRunsNothingForUnassignedOrMismatchedSlots() async {
    let ex = RecordingExecutor()
    let bad = ControlsMapping(push: ControlAction(kind: .volume, step: 2), doublePush: ControlAction(),
                              longPush: ControlAction(kind: .shortcut, name: "  "), turn: ControlAction(kind: .applescript, script: "beep"))
    for c in [ControlCommand(id: "1", gesture: .push), ControlCommand(id: "2", gesture: .doublePush),
              ControlCommand(id: "3", gesture: .longPush), ControlCommand(id: "4", gesture: .turn, steps: 1)] {
        #expect(await ControlDispatcher.perform(c, mapping: bad, on: ex) == .unassigned)
    }
    #expect(await ex.calls.isEmpty)
}

@Test func dispatcherNeedsAccessibilityOnlyForEventKinds() async {
    let ex = RecordingExecutor()
    await ex.set(granted: false)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "1", gesture: .push), mapping: mapping, on: ex) == .needsAccessibility)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "2", gesture: .turn, steps: 1), mapping: mapping, on: ex) == .ran)
    #expect(await ex.calls == ["volume 3"])
}

@Test func dispatcherDropsStaleTurnsSoonerThanPresses() async {
    let ex = RecordingExecutor()
    #expect(await ControlDispatcher.perform(ControlCommand(id: "1", gesture: .turn, steps: 1), mapping: mapping, on: ex,
                                            age: .milliseconds(2_001)) == .stale)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "2", gesture: .doublePush), mapping: mapping, on: ex,
                                            age: .milliseconds(2_001)) == .ran)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "3", gesture: .doublePush), mapping: mapping, on: ex,
                                            age: .milliseconds(5_001)) == .stale)
    await ex.set(succeeds: false)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "4", gesture: .doublePush), mapping: mapping, on: ex) == .failed)
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
