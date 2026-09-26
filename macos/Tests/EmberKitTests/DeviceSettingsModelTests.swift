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

private let sampleApps = [
    AppInfo(name: "Time", enabled: true, origin: "builtin", present: true, slot: 0),
    AppInfo(name: "ember", enabled: true, origin: "pushed", present: true, slot: 1),
    AppInfo(name: "Date", enabled: true, origin: "builtin", present: true, slot: 2),
    AppInfo(name: "Battery", enabled: false, origin: "builtin", present: true),
    AppInfo(name: "gone", enabled: true, origin: nil, present: false, slot: 3),
]

@Test func nativeAppsListsBuiltinsOnly() {
    #expect(NativeAppsPlan.listed(sampleApps).map(\.name) == ["Time", "Date", "Battery"])
}

@Test func toggleOffNamesOnlyThatAppAsDisabled() {
    let u = NativeAppsPlan.toggle("Date", enabled: false, in: sampleApps)
    #expect(u.disabled == ["Date"])
    #expect(u.order == ["Time", "ember"])
    #expect(Set(u.order).isDisjoint(with: u.disabled))
}

@Test func toggleOnOrdersItWithoutDisablingOthersAgain() {
    let u = NativeAppsPlan.toggle("Battery", enabled: true, in: sampleApps)
    #expect(u.disabled.isEmpty)
    #expect(u.order == ["Time", "ember", "Date", "Battery"])
}

@Test func moveKeepsPushedAppsInPlace() {
    // Move "Date" (listed index 1) to the top.
    let (apps, u) = NativeAppsPlan.move(in: sampleApps, fromOffsets: IndexSet(integer: 1), toOffset: 0)
    #expect(apps.map(\.name) == ["Date", "ember", "Time", "Battery", "gone"])
    #expect(u.order == ["Date", "ember", "Time"])
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
        let (status, text) = lock.withLock {
            _log.append((method, path, body))
            return responses["\(method) \(path)"] ?? (method == "GET" ? (404, "") : (200, ""))
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
    let (m, _) = makeModel(fake)
    await m.load()
    #expect(m.isLoaded)
    #expect(m.supportsNG11)
    #expect(m.displayPower == true)
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
        "GET /v1/device/display": (200, #"{"overlay":null}"#),
    ]
    let (m, _) = makeModel(fake)
    await m.load()
    #expect(m.isLoaded)
    #expect(!m.supportsNG11)
    #expect(m.audio == .unsupported)
    #expect(m.displayPower == nil)
    #expect(m.hasBuzzer)
    #expect(m.transitions == DeviceKnownValues.fallbackTransitions)
}

@MainActor @Test func noBuzzerIsNotUnsupported() async {
    let fake = ngServer()
    fake.responses["GET /v1/device/audio/melodies"] = (503, #"{"error":"unavailable"}"#)
    let (m, _) = makeModel(fake)
    await m.load()
    #expect(m.audio == .noOutput)
}

@MainActor @Test func unreachableSettingsSkipTheRest() async {
    let fake = FakeClock()
    fake.responses["GET /v1/device/settings"] = (502, #"{"error":"clock unreachable"}"#)
    let (m, _) = makeModel(fake)
    await m.load()
    #expect(!m.isLoaded)
    #expect(m.loadError != nil)
    #expect(fake.paths == ["GET /v1/device/settings"])
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

@MainActor @Test func displayPowerRollsBackOnFailure() async {
    let fake = ngServer()
    fake.responses["PUT /v1/device/display/power"] = (404, "")
    let (m, _) = makeModel(fake)
    await m.load()
    await m.setDisplayPower(false)
    #expect(m.displayPower == true)
    #expect(m.actionErrors[.displayPower] == .featureOff)
}

@MainActor @Test func configureSameServerIsANoOp() async {
    let fake = ngServer()
    let (m, _) = makeModel(fake)
    await m.load()
    let before = m.settings
    m.configure(service: m.service)
    #expect(m.settings === before)
}
