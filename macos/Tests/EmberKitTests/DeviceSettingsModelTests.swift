import Testing
import Foundation
@testable import EmberKit

// MARK: Patch

@Test func patchSendsOnlyChangedKeys() {
    var old = DeviceSettings()
    old.brightness = 100
    old.autoTransition = true
    old.uppercase = true
    var new = old
    new.brightness = 180
    let patch = new.patch(from: old)
    #expect(patch == ["brightness": .int(180)])
}

@Test func patchNullsAnInheritedColor() {
    var old = DeviceSettings()
    old.timeColor = "#FF0000"
    old.textColor = "#FFFFFF"
    var new = old
    new.timeColor = nil
    #expect(new.patch(from: old) == ["timeColor": .null])
}

@Test func patchNeverNullsANonColorKey() {
    var old = DeviceSettings()
    old.brightness = 10
    let new = DeviceSettings()
    #expect(new.patch(from: old).isEmpty)
}

@Test func patchSendsAChangedNestedObjectWhole() {
    var old = DeviceSettings()
    old.scroll = ScrollSettings(mode: "wrap", speed: 100)
    var new = old
    new.scroll?.speed = 80
    #expect(new.patch(from: old) == ["scroll": .object(["mode": .string("wrap"), "speed": .int(80)])])
}

@Test func patchEncodesExplicitNull() throws {
    let data = try JSONEncoder().encode(JSONValue.object(["timeColor": .null, "uppercase": .bool(false)]))
    let obj = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
    #expect(obj["timeColor"] is NSNull)
    #expect(obj["uppercase"] as? Bool == false)
}

@Test func ng11SupportFollowsTheSoundKeys() throws {
    let old = try JSONDecoder().decode(DeviceSettings.self, from: Data(#"{"brightness":10,"uppercase":true}"#.utf8))
    #expect(!old.serverSupportsNG11)
    let new = try JSONDecoder().decode(DeviceSettings.self, from: Data(#"{"brightness":10,"soundEnabled":true}"#.utf8))
    #expect(new.serverSupportsNG11)
}

// MARK: Units and options

@Test func brightnessPercentRoundTrips() {
    #expect(DeviceUnits.brightnessPercent(raw: 0) == 0)
    #expect(DeviceUnits.brightnessPercent(raw: 255) == 100)
    #expect(DeviceUnits.brightnessPercent(raw: 120) == 47)
    #expect(DeviceUnits.brightnessRaw(percent: 100) == 255)
    #expect(DeviceUnits.brightnessRaw(percent: 47) == 120)
    #expect(DeviceUnits.brightnessRaw(percent: 150) == 255)
    for p in 0...100 {
        #expect(DeviceUnits.brightnessPercent(raw: DeviceUnits.brightnessRaw(percent: p)) == p)
    }
}

@Test func timeStylesCoverNGModes() {
    #expect(ClockTimeStyle.allCases.map(\.rawValue) == Array(0...6))
    #expect(ClockTimeStyle.centered.showsSecondsAndAmPm)
    #expect(!ClockTimeStyle.calendarBarBelow.showsSecondsAndAmPm)
    #expect(!ClockTimeStyle.binary.drawsWeekdayBar)
    #expect(ClockTimeStyle.notchedBarAbove.drawsCalendarBox)
}

// MARK: Native apps

/// NG 1.1.2 `GET /api/v1/apps` as the server relays it: arranged apps
/// first, an Ember tile that's away between pushes (origin null, present
/// false, slot kept), a script, and a module (no enabled/inLoop/slot).
private let appsJSON = #"""
[{"name":"Time","enabled":true,"inLoop":true,"present":true,"slot":0,"origin":"builtin"},
 {"name":"ember","enabled":true,"inLoop":true,"present":true,"slot":1,"origin":"pushed"},
 {"name":"Date","enabled":true,"inLoop":true,"present":true,"slot":2,"origin":"builtin"},
 {"name":"ember-weather","enabled":true,"inLoop":true,"present":false,"slot":3,"origin":null},
 {"name":"Battery","enabled":false,"inLoop":false,"present":true,"slot":null,"origin":"builtin"},
 {"name":"clockface","enabled":true,"inLoop":true,"present":true,"slot":null,"origin":"script",
  "skipped":false,"headless":false,"error":null,"meta":{"name":"","desc":"","author":"","version":"","icons":[]}},
 {"name":"mathlib","origin":"module","import":"mathlib","error":null,
  "meta":{"name":"","desc":"","author":"","version":"","icons":[]}}]
"""#

private let sampleApps = try! JSONDecoder().decode([AppInfo].self, from: Data(appsJSON.utf8))

@Test func nativeAppsListsBuiltinsOnly() {
    #expect(NativeAppsPlan.listed(sampleApps).map(\.name) == ["Time", "Date", "Battery"])
}

@Test func toggleOffNamesOnlyThatAppAsDisabled() {
    let u = NativeAppsPlan.toggle("Date", enabled: false, in: sampleApps)
    #expect(u.disabled == ["Date"])
    #expect(u.order == ["Time", "ember", "ember-weather", "clockface"])
    #expect(Set(u.order).isDisjoint(with: u.disabled))
}

@Test func toggleOnOrdersItWithoutDisablingOthersAgain() {
    let u = NativeAppsPlan.toggle("Battery", enabled: true, in: sampleApps)
    #expect(u.disabled.isEmpty)
    #expect(u.order == ["Time", "ember", "Date", "ember-weather", "Battery", "clockface"])
}

@Test func orderNeverNamesModules() {
    #expect(sampleApps.last?.origin == "module")
    for u in [NativeAppsPlan.toggle("Date", enabled: false, in: sampleApps),
              NativeAppsPlan.toggle("Battery", enabled: true, in: sampleApps),
              NativeAppsPlan.move(in: sampleApps, fromOffsets: [1], toOffset: 0).update] {
        #expect(!u.order.contains("mathlib"))
    }
}

@Test func anAwayEmberTileKeepsItsSlot() {
    let u = NativeAppsPlan.toggle("Time", enabled: false, in: sampleApps)
    #expect(u.order == ["ember", "Date", "ember-weather", "clockface"])
}

@Test func moveKeepsPushedAppsInPlace() {
    // Move "Date" (listed index 1) to the top.
    let (apps, u) = NativeAppsPlan.move(in: sampleApps, fromOffsets: IndexSet(integer: 1), toOffset: 0)
    #expect(apps.map(\.name) == ["Date", "ember", "Time", "ember-weather", "Battery", "clockface", "mathlib"])
    #expect(u.order == ["Date", "ember", "Time", "ember-weather", "clockface"])
    #expect(u.disabled.isEmpty)
}

@Test func moveDownwards() {
    let (apps, _) = NativeAppsPlan.move(in: sampleApps, fromOffsets: IndexSet(integer: 0), toOffset: 3)
    #expect(NativeAppsPlan.listed(apps).map(\.name) == ["Date", "Battery", "Time"])
}

@Test func appInfoDecodesPresentAndSlot() throws {
    let json = #"[{"name":"Time","enabled":true,"inLoop":true,"present":true,"slot":0,"origin":"builtin"},{"name":"mod","origin":"module"}]"#
    let apps = try JSONDecoder().decode([AppInfo].self, from: Data(json.utf8))
    #expect(apps[0].present == true && apps[0].slot == 0)
    #expect(apps[1].enabled && apps[1].slot == nil)
}

// MARK: Model

/// A fake server for /v1/device/*: canned GETs, a log of writes.
private final class FakeClock: @unchecked Sendable {
    private let lock = NSLock()
    var responses: [String: (Int, String)] = [:]
    /// Requests for this route wait for `gate` (once armed).
    var gatedRoute: String?
    let gate = DispatchSemaphore(value: 0)
    private var _log: [(String, String, [String: Any])] = []
    var log: [(method: String, path: String, body: [String: Any])] {
        lock.withLock { _log.map { ($0.0, $0.1, $0.2) } }
    }
    var paths: [String] { log.map { "\($0.method) \($0.path)" } }

    func handle(_ req: URLRequest) -> (HTTPURLResponse, Data) {
        let path = req.url!.path
        let method = req.httpMethod ?? "GET"
        let data = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let body = (try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
        let key = "\(method) \(path)"
        let gated = lock.withLock { () -> Bool in
            guard gatedRoute == key else { return false }
            gatedRoute = nil
            return true
        }
        if gated { gate.wait() }
        let (status, text) = lock.withLock {
            _log.append((method, path, body))
            return responses[key] ?? (method == "GET" ? (404, "") : (200, ""))
        }
        return (okResponse(req.url!, status: status), Data(text.utf8))
    }
}

@MainActor
private func makeModel(_ fake: FakeClock) -> (DeviceSettingsModel, ManualClock) {
    let clock = ManualClock()
    let client = stubbedClient(token: "t") { fake.handle($0) }
    let m = DeviceSettingsModel(service: DeviceService(client: client), debounce: .milliseconds(600),
                                sleep: clock.sleepFn, now: { Date() })
    return (m, clock)
}

private func ngServer() -> FakeClock {
    let f = FakeClock()
    f.responses = [
        "GET /v1/device/settings": (200, ##"{"brightness":120,"soundEnabled":true,"buzzerVolume":80,"timeColor":"#FF0000","autoTransition":true}"##),
        "GET /v1/device/display": (200, #"{"overlay":null,"power":true}"#),
        "GET /v1/device/sensors": (200, #"{"temp_offset":-9,"hum_offset":null}"#),
        "GET /v1/device/config": (200, #"{"base_url":"http://clock","source":"store"}"#),
        "GET /v1/device/capabilities": (200, #"{"transitions":["Fade"],"overlays":["rain"],"audio":{"buzzer":true}}"#),
        "GET /v1/device/apps": (200, #"[{"name":"Time","enabled":true,"inLoop":true,"origin":"builtin","present":true}]"#),
        "GET /v1/device/buttons": (200, #"{"configured":true,"seconds_since":30}"#),
        "GET /v1/device/stats": (200, #"{"version":"1.1.2","temperature":21.5}"#),
        "GET /v1/device/audio/melodies": (200, #"{"melodies":[{"name":"bell","rtttl":"b:d=4:c","bytes":7,"notes":1,"durationMs":300,"valid":true},{"name":"bad","rtttl":"x","bytes":1,"notes":0,"durationMs":0,"valid":false}],"usedBytes":1,"totalBytes":2}"#),
    ]
    return f
}

@MainActor @Test func loadReadsEverythingSequentially() async {
    let fake = ngServer()
    let live = LiveModel()
    live.configure(client: stubbedClient { req in (okResponse(req.url!), Data()) })
    let m = DeviceSettingsModel(service: DeviceService(client: stubbedClient(token: "t") { fake.handle($0) }),
                                live: live, debounce: .milliseconds(600), sleep: ManualClock().sleepFn, now: { Date() })
    await m.load()
    #expect(m.isLoaded)
    #expect(m.supportsNG11)
    // #149: the overlay read's power goes to the one display-power value.
    #expect(live.displayPower == true)
    #expect(m.transitions == ["Fade"])
    #expect(m.overlays == ["rain"])
    #expect(m.config?.baseURL == "http://clock")
    #expect(m.nativeApps.map(\.name) == ["Time"])
    #expect(m.buttons?.configured == true)
    #expect(m.melodies.map(\.name) == ["bell"])
    #expect(m.audio == .available)
    #expect(fake.paths.first == "GET /v1/device/settings")
    #expect(fake.paths.count == 9)
}

@MainActor @Test func settingsSaveSendsThePatchOnly() async throws {
    let fake = ngServer()
    let (m, clock) = makeModel(fake)
    await m.load()
    m.settings.draft.brightness = 200
    m.settings.draft.timeColor = nil
    m.settings.scheduleSave()
    await clock.advance(by: .milliseconds(600))
    let put = try #require(fake.log.last { $0.method == "PUT" })
    #expect(put.path == "/v1/device/settings")
    #expect(put.body.count == 2)
    #expect(put.body["brightness"] as? Int == 200)
    #expect(put.body["timeColor"] is NSNull)
    #expect(m.settings.status == .saved)
    // A second edit diffs against what was just saved.
    m.settings.draft.brightness = 10
    m.settings.scheduleSave()
    await clock.advance(by: .milliseconds(600))
    let second = try #require(fake.log.last { $0.method == "PUT" })
    #expect(second.body.count == 1)
    #expect(second.body["brightness"] as? Int == 10)
}

@MainActor @Test func oldServerHidesNG11AndAudio() async {
    let fake = FakeClock()
    fake.responses = [
        "GET /v1/device/settings": (200, #"{"brightness":120,"uppercase":true}"#),
        // 0.27.1 relays NG's display body raw, power included.
        "GET /v1/device/display": (200, #"{"overlay":null,"power":true}"#),
    ]
    let (m, _) = makeModel(fake)
    await m.load()
    #expect(m.isLoaded)
    #expect(!m.supportsNG11)
    #expect(m.audio == .unsupported)
    #expect(!m.supportsControlRoutes)
    #expect(!m.firmwareTooOld)
    #expect(m.hasBuzzer)
    #expect(m.transitions == DeviceKnownValues.fallbackTransitions)
}

@MainActor @Test func noBuzzerIsNotUnsupported() async {
    let fake = ngServer()
    fake.responses["GET /v1/device/audio/melodies"] = (503, #"{"error":"unavailable"}"#)
    let (m, _) = makeModel(fake)
    await m.load()
    #expect(m.audio == .noOutput)
    #expect(m.supportsControlRoutes)
}

@MainActor @Test func newServerWithOldFirmwareBlamesTheFirmware() async {
    let fake = ngServer()
    fake.responses["GET /v1/device/settings"] = (200, #"{"brightness":120}"#)
    let (m, _) = makeModel(fake)
    await m.load()
    #expect(!m.supportsNG11)
    #expect(m.firmwareTooOld)
}

/// Polls until `done` holds (the stub answers on another thread).
@MainActor private func eventually(_ done: () -> Bool) async {
    for _ in 0..<400 where !done() { try? await Task.sleep(for: .milliseconds(5)) }
}

@MainActor @Test func editDuringAnInFlightLoadSendsOnlyTheEdit() async throws {
    let fake = ngServer()
    let (m, clock) = makeModel(fake)
    await m.load()
    // The Pomodoro takeover flips these on the clock meanwhile.
    fake.responses["GET /v1/device/settings"] = (200, ##"{"brightness":120,"soundEnabled":true,"buzzerVolume":80,"timeColor":"#FF0000","autoTransition":false,"blockNavigation":true}"##)
    fake.gatedRoute = "GET /v1/device/settings"
    let refocus = Task { await m.load() }
    await eventually { fake.gatedRoute == nil }   // the GET is in flight
    m.settings.draft.brightness = 200
    m.settings.scheduleSave()
    fake.gate.signal()
    await refocus.value                             // discarded: an edit is pending
    #expect(m.settings.applied?.autoTransition == true)
    await clock.advance(by: .milliseconds(600))
    await eventually { fake.log.contains { $0.method == "PUT" } }
    let put = try #require(fake.log.last { $0.method == "PUT" })
    #expect(put.body.keys.sorted() == ["brightness"])
}

@MainActor @Test func cancelledLoadDoesNotHoldOffTheNext() async {
    let fake = ngServer()
    let (m, _) = makeModel(fake)
    fake.gatedRoute = "GET /v1/device/apps"
    let first = Task { await m.load() }
    await eventually { fake.gatedRoute == nil }   // secondary reads under way
    first.cancel()
    fake.gate.signal()
    await first.value
    let before = fake.paths.filter { $0 == "GET /v1/device/apps" }.count
    await m.load()                                  // not throttled by the cut-short one
    #expect(fake.paths.filter { $0 == "GET /v1/device/apps" }.count == before + 1)
}

@MainActor @Test func forcedLoadQueuesBehindARunningOne() async {
    let fake = ngServer()
    let (m, _) = makeModel(fake)
    await m.load()
    let settingsGets = { fake.paths.filter { $0 == "GET /v1/device/settings" }.count }
    fake.gatedRoute = "GET /v1/device/apps"
    let running = Task { await m.load(force: true) }
    await eventually { fake.gatedRoute == nil }
    await m.load(force: true)                       // returns at once, queued
    let during = settingsGets()
    fake.gate.signal()
    await running.value
    await eventually { settingsGets() == during + 1 && !m.isLoading }
    #expect(settingsGets() == during + 1)
}

@MainActor @Test func unreachableSettingsSkipTheRest() async {
    let fake = FakeClock()
    fake.responses["GET /v1/device/settings"] = (502, #"{"error":"clock unreachable"}"#)
    let (m, _) = makeModel(fake)
    await m.load()
    #expect(!m.isLoaded)
    #expect(m.loadError != nil)
    // Only the clock's address follows, so Status can show where it looked.
    #expect(fake.paths == ["GET /v1/device/settings", "GET /v1/device/config"])
    #expect(m.config?.baseURL == nil)
}

@MainActor @Test func refocusSkipsSecondaryWhileFresh() async {
    let fake = ngServer()
    let (m, _) = makeModel(fake)
    await m.load()
    let first = fake.paths.count
    await m.load()
    #expect(fake.paths.count == first + 1)          // settings only
    await m.load(force: true)
    #expect(fake.paths.count == first + 1 + 9)
}

@MainActor @Test func toggleAppPutsPlanAndRollsBackOnFailure() async throws {
    let fake = ngServer()
    let (m, _) = makeModel(fake)
    await m.load()
    await m.setApp("Time", enabled: false)
    let put = try #require(fake.log.last { $0.method == "PUT" })
    #expect(put.path == "/v1/device/apps")
    #expect(put.body["disabled"] as? [String] == ["Time"])
    #expect(m.nativeApps.first?.enabled == false)
    #expect(m.writes.status == .saved)

    fake.responses["PUT /v1/device/apps"] = (500, #"{"error":"boom"}"#)
    await m.setApp("Time", enabled: true)
    #expect(m.nativeApps.first?.enabled == false)
    #expect(m.actionErrors[.apps] != nil)
    if case .error = m.writes.status {} else { Issue.record("expected an error status") }
}

@MainActor @Test func configureSameServerIsANoOp() async {
    let fake = ngServer()
    let (m, _) = makeModel(fake)
    await m.load()
    let before = m.settings
    m.configure(service: m.service)
    #expect(m.settings === before)
}
