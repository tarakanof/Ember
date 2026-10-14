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

@Test func controlCommandsDecodeEveryKindAndTolerateUnknowns() throws {
    let json = Data(#"""
    {"commands":[
      {"id":"k1","kind":"volume","delta":6,"age_ms":12},
      {"id":"k2","kind":"scroll","axis":"horizontal","delta":-3},
      {"id":"k3","kind":"media","key":"next"},
      {"id":"k4","kind":"shortcut","name":"Focus"},
      {"id":"k5","kind":"applescript","script":"beep"},
      {"id":"k6","kind":"mute","age_ms":-4},
      {"id":"k7","kind":"teleport","key":"warp"}
    ]}
    """#.utf8)
    let c = try JSONDecoder().decode(ControlCommands.self, from: json).commands
    #expect(c.map(\.kind) == [.volume, .scroll, .media, .shortcut, .applescript, .mute, .none])
    #expect(c[0].delta == 6 && c[0].ageMs == 12)
    #expect(c[1].axis == .horizontal && c[1].delta == -3)
    #expect(c[2].key == .next)
    #expect(c[3].name == "Focus" && c[4].script == "beep")
    #expect(c[5].ageMs == 0 && c[6].key == nil)
}

@Test func controlsConfigDecodesTheServerAnswerAndNeverSendsMacsBack() throws {
    let json = Data(#"""
    {"target":"M4","push":{"kind":"media","key":"play_pause"},"double_push":{"kind":"none"},
     "long_push":{"kind":"shortcut","name":"Focus"},"turn":{"kind":"scroll","lines":3,"axis":"vertical"},
     "macs":[{"name":"M4","listening":true,"last_poll":"2026-10-14T10:00:00Z"},{"name":"M5","listening":false}]}
    """#.utf8)
    let cfg = try JSONDecoder().decode(ControlsConfig.self, from: json)
    #expect(cfg.target == "M4" && cfg.longPush.name == "Focus" && cfg.turn.axis == .vertical)
    #expect(cfg.macs.map(\.name) == ["M4", "M5"] && cfg.macs[0].listening)
    #expect(cfg.needsEventAccess)
    let sent = try JSONSerialization.jsonObject(with: JSONEncoder().encode(cfg)) as? [String: Any]
    #expect(sent?["macs"] == nil && sent?["double_push"] != nil)
}

@Test func controlsConfigDefaultsMatchTheServer() throws {
    let cfg = try JSONDecoder().decode(ControlsConfig.self, from: Data("{}".utf8))
    #expect(cfg == ControlsConfig())
    #expect(cfg.push == ControlAction(kind: .media, key: .playPause))
    #expect(cfg.turn == ControlAction(kind: .volume, step: 2))
}

@Test func controlsTargetChoicesIncludeThisMacAndTheSavedTarget() {
    let cfg = ControlsConfig(target: "Old Mac", macs: [ControlsMac(name: "M5", listening: true)])
    #expect(cfg.targetChoices(thisMac: "M4") == ["M4", "M5", "Old Mac"])
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

@Test func dispatcherMapsEachKindToItsExecutorCall() async {
    let ex = RecordingExecutor()
    let commands = [
        ControlCommand(id: "1", kind: .volume, delta: -4),
        ControlCommand(id: "2", kind: .mute),
        ControlCommand(id: "3", kind: .media, key: .previous),
        ControlCommand(id: "4", kind: .scroll, axis: .vertical, delta: 6),
        ControlCommand(id: "5", kind: .shortcut, name: "Focus"),
        ControlCommand(id: "6", kind: .applescript, script: "beep"),
    ]
    for c in commands { #expect(await ControlDispatcher.perform(c, on: ex) == .ran) }
    #expect(await ex.calls == ["volume -4", "mute", "media previous", "scroll vertical 6", "shortcut Focus",
                               "applescript beep"])
}

@Test func dispatcherNeedsAccessibilityOnlyForEventKinds() async {
    let ex = RecordingExecutor()
    await ex.set(granted: false)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "1", kind: .media, key: .next), on: ex) == .needsAccessibility)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "2", kind: .scroll, axis: .vertical, delta: 1), on: ex)
        == .needsAccessibility)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "3", kind: .volume, delta: 2), on: ex) == .ran)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "4", kind: .shortcut, name: "Focus"), on: ex) == .ran)
    #expect(await ex.calls == ["volume 2", "shortcut Focus"])
}

@Test func dispatcherDropsStaleStepsSoonerThanPresses() async {
    let ex = RecordingExecutor()
    #expect(await ControlDispatcher.perform(ControlCommand(id: "1", kind: .volume, delta: 2), on: ex,
                                            age: .milliseconds(2_001)) == .stale)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "2", kind: .mute), on: ex,
                                            age: .milliseconds(2_001)) == .ran)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "3", kind: .mute), on: ex,
                                            age: .milliseconds(5_001)) == .stale)
    #expect(await ex.calls == ["mute"])
}

@Test func dispatcherRejectsIncompleteCommandsAndReportsFailures() async {
    let ex = RecordingExecutor()
    #expect(await ControlDispatcher.perform(ControlCommand(id: "1", kind: .none), on: ex) == .unsupported)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "2", kind: .media), on: ex) == .unsupported)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "3", kind: .shortcut), on: ex) == .unsupported)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "4", kind: .volume), on: ex) == .unsupported)
    await ex.set(succeeds: false)
    #expect(await ControlDispatcher.perform(ControlCommand(id: "5", kind: .mute), on: ex) == .failed)
}

@MainActor
@Test func controlsListenerPollsAsThisMacAndRunsCommandsInOrder() async {
    let ex = RecordingExecutor()
    let listener = ControlsListener(executor: ex, sleep: { _ in try Task.checkCancellation() })
    let source = FakeControls([.success([
        ControlCommand(id: "1", kind: .media, key: .playPause),
        ControlCommand(id: "2", kind: .volume, delta: 4),
    ])])
    listener.start(source: source, mac: "M4")
    for _ in 0..<500 where await source.macs.count < 2 { try? await Task.sleep(for: .milliseconds(2)) }
    #expect(await ex.calls == ["media play_pause", "volume 4"])
    #expect(await source.macs.allSatisfy { $0 == "M4" })
    listener.stop()
    await listener.join()
}

@MainActor
@Test func controlsListenerCountsLocalDelayTowardsAge() async {
    let ex = RecordingExecutor()
    let listener = ControlsListener(executor: ex)
    let out = await listener.execute([ControlCommand(id: "1", kind: .volume, delta: 2, ageMs: 500)],
                                     received: .now - .seconds(2))
    #expect(out == [.stale])
    #expect(await ex.calls.isEmpty)
}

@Test func controlsClientDecodesTheLongPoll() async throws {
    let seen = LockedBox()
    let client = stubbedClient { req in
        seen.add(req.url!.absoluteString)
        return (okResponse(req.url!), Data(#"{"commands":[{"id":"k1","kind":"mute","age_ms":3}]}"#.utf8))
    }
    let got = try await ControlsClient(client: client).commands(mac: "Dmitry's Mac", wait: 25)
    #expect(got == [ControlCommand(id: "k1", kind: .mute, ageMs: 3)])
    #expect(seen.paths.first?.contains("/v1/controls/commands?mac=Dmitry's%20Mac&wait=25") == true)
}

@Test func controlsActionGoldenDecodes() throws {
    let obj = try JSONSerialization.jsonObject(with: try devicesGolden("controls_action")) as? [String: Any]
    #expect(obj?["status"] as? String == "queued")
    #expect(ControlKind(rawValue: obj?["kind"] as? String ?? "") != nil)
}
