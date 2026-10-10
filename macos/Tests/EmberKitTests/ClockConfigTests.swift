import Foundation
import Testing
@testable import EmberKit

private func fixture(_ name: String) throws -> Data {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .appendingPathComponent("cmd/ember/testdata/clock/\(name).json")
    return try Data(contentsOf: url)
}

private func decodeFixture(_ name: String) throws -> ClockConfig {
    try JSONDecoder().decode(ClockConfig.self, from: try fixture(name))
}

@Test func facadeDefaultFixtureDecodes() throws {
    let c = try decodeFixture("config_default")
    #expect(c.schema == 1)
    #expect(c.apps.agents == ClockConfig.Agents(usageCards: true, usagePerModel: true, hiddenTools: []))
    #expect(c.apps.focus == ClockConfig.Focus(focusColor: "#FF0000", breakColor: "#00FF00"))
    #expect(c.apps.weather == ClockConfig.Weather())
    #expect(c.apps.calendar == ClockConfig.Calendar())
    #expect(c.rotation == ClockConfig.Rotation(order: ["time", "date"], disabled: ["hum"]))
}

@Test func facadeCustomFixtureDecodes() throws {
    let c = try decodeFixture("config_custom")
    #expect(c.apps.agents.hiddenTools == ["codex", "t3"])
    #expect(!c.apps.agents.usageCards && !c.apps.agents.usagePerModel)
    #expect(c.apps.focus.breakColor == "#445566")
    let w = c.apps.weather
    #expect(!w.on && w.nativeIcon && !w.forecast && w.forecastHours == 6)
    #expect(!w.air && !w.moon && !w.overlay)
    #expect(w.popups == ClockConfig.Popups(onChange: false, sun: false, severe: false, nativeIcons: true,
                                           intervalMinutes: 0, durationSeconds: 8))
    #expect(w.iconIds == ["clear": "123"])
    #expect(c.apps.calendar == ClockConfig.Calendar(on: false, tileLeadMinutes: 15, popupLeadMinutes: 0))
    #expect(c.rotation == ClockConfig.Rotation(order: ["date", "time"], disabled: ["hum"]))
}

@Test func facadeFixtureRoundTripsThroughEncoding() throws {
    for name in ["config_default", "config_custom"] {
        let c = try decodeFixture(name)
        let again = try JSONDecoder().decode(ClockConfig.self, from: JSONEncoder().encode(c))
        #expect(again == c)
    }
}

@Test func facadeNullRotationDecodes() throws {
    var obj = try #require(try JSONSerialization.jsonObject(with: fixture("config_default")) as? [String: Any])
    obj["rotation"] = NSNull()
    let c = try JSONDecoder().decode(ClockConfig.self, from: JSONSerialization.data(withJSONObject: obj))
    #expect(c.rotation == nil)
}

@Test func facadePatchNamesOnlyTheChangedLeaf() throws {
    let old = try decodeFixture("config_default")
    var new = old
    new.apps.weather.popups.sun = false
    #expect(new.patch(from: old) == ["apps": .object(["weather": .object(["popups": .object(["sun": .bool(false)])])])])
}

@Test func facadePatchOfAnUnchangedConfigIsEmpty() throws {
    let c = try decodeFixture("config_custom")
    #expect(c.patch(from: c).isEmpty)
}

@Test func facadePatchSendsArraysMapsAndRotationWhole() throws {
    let old = try decodeFixture("config_custom")
    var new = old
    new.apps.agents.hiddenTools = ["codex"]
    new.apps.weather.iconIds["rain"] = "72"
    new.rotation = ClockConfig.Rotation(order: ["time", "date"], disabled: ["hum"])
    #expect(new.patch(from: old) == [
        "apps": .object([
            "agents": .object(["hidden_tools": .array([.string("codex")])]),
            "weather": .object(["icon_ids": .object(["clear": .string("123"), "rain": .string("72")])]),
        ]),
        "rotation": .object(["order": .array([.string("time"), .string("date")]), "disabled": .array([.string("hum")])]),
    ])
}

@Test func facadePatchCanDropAnIconOverride() throws {
    let old = try decodeFixture("config_custom")
    var new = old
    new.apps.weather.iconIds = [:]
    #expect(new.patch(from: old) == ["apps": .object(["weather": .object(["icon_ids": .object([:])])])])
}

@Test func weatherSliceMapsOntoTheOldConfig() throws {
    let w = try decodeFixture("config_custom").apps.weather
    var old = WeatherConfig(enabled: true, refreshMinutes: 15, airPopupThreshold: 60)
    w.apply(to: &old)
    #expect(old.enabled && old.refreshMinutes == 15 && old.airPopupThreshold == 60)
    #expect(!old.rotateInApps && old.tileNativeIcons && !old.forecastTile && old.forecastHours == 6)
    #expect(!old.airTile && !old.moonPhase && !old.overlay)
    #expect(!old.popupOnChange && !old.sunPopups && !old.severeAlert && old.useNativeIcons)
    #expect(old.popupIntervalMinutes == 0 && old.popupDurationSeconds == 8 && old.iconIds == ["clear": "123"])
    var back = ClockConfig.Weather()
    back.take(from: old)
    #expect(back == w)
}

@MainActor @Test func otherSlicesMapOntoTheirOldConfigs() throws {
    let apps = try decodeFixture("config_custom").apps
    var usage = UsageConfig(usageThresholdPct: 40)
    apps.agents.apply(to: &usage)
    #expect(!usage.usageWidget && !usage.usagePerModel && usage.usageThresholdPct == 40)
    var pomo = SettingsModels.defaultPomoConfig
    apps.focus.apply(to: &pomo)
    #expect(pomo.focusColor == "#112233" && pomo.breakColor == "#445566" && pomo.focusMinutes == 25)
    var meetings = MeetingsConfig(chime: false, icsUrlsConfigured: 2)
    apps.calendar.apply(to: &meetings)
    #expect(!meetings.enabled && meetings.tileLeadMinutes == 15 && meetings.popupLeadMinutes == 0)
    #expect(!meetings.chime && meetings.icsUrlsConfigured == 2)

    var agents = ClockConfig.Agents(hiddenTools: ["codex"])
    agents.take(from: usage)
    #expect(agents == ClockConfig.Agents(usageCards: false, usagePerModel: false, hiddenTools: ["codex"]))
}

@Test func clockRecordIsTheAwtrixNGDevice() throws {
    let body = #"""
    {"devices":[
     {"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"aa:bb","name":"Knob","created_at":"2026-10-01T00:00:00Z","config_version":3,"rotation_pending":false,"rotated_at":null,"last_checkin":null},
     {"id":"clock-05ffb8","kind":"awtrix-ng","hw_id":"e868e705ffb8","name":"Clock 05FFB8","created_at":"2026-10-02T00:00:00Z","config_version":12345,"rotation_pending":false,"rotated_at":null,"last_checkin":null,"last_seen":{"seen_at":"2026-10-10T10:00:00Z","fw":"1.1.2","ip":"192.168.0.66","rssi":-70,"uptime_s":5}}
    ]}
    """#
    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .iso8601
    let list = try decoder.decode(KnobDeviceList.self, from: Data(body.utf8))
    #expect(KnobDevice.clock(in: list.devices)?.id == "clock-05ffb8")
    #expect(KnobDevice.current(in: list.devices)?.id == "knob-61fc8c")
    #expect(KnobDevice.clock(in: [list.devices[0]]) == nil)
}

@Test func treeKeysTheClockByItsRecordAndMovesPlaceholderRoutes() {
    let tree = SettingsTree(devices: [SettingsDevice(id: "clock-05ffb8", kind: .clock, name: "Clock", state: .ready)])
    #expect(tree.devices.first?.apps.contains(.device("clock-05ffb8", .app(.weather))) == true)
    #expect(tree.resolve(SettingsRoute(stored: "device/clock/app/weather")) == .device("clock-05ffb8", .app(.weather)))
    let placeholder = SettingsTree(devices: [SettingsDevice(id: "clock", kind: .clock, name: "Clock", state: .ready)])
    #expect(placeholder.resolve(SettingsRoute(stored: "device/clock-05ffb8/apps")) == .device("clock", .apps))
}

private final class FacadeServer: @unchecked Sendable {
    private let lock = NSLock()
    private var _log: [(key: String, body: Data)] = []
    private var _responses: [String: (Int, String)] = [:]
    private var _gates: [String: DispatchSemaphore] = [:]
    private var _arrived: Set<String> = []

    init(config: String) {
        _responses["GET /v1/devices/clock-a/config"] = (200, config)
        _responses["PUT /v1/devices/clock-a/config"] = (200, config)
    }

    var log: [String] { lock.withLock { _log.map(\.key) } }
    func bodies(_ key: String) -> [[String: Any]] {
        lock.withLock { _log.filter { $0.key == key }.map(\.body) }
            .map { (try? JSONSerialization.jsonObject(with: $0) as? [String: Any]) ?? [:] }
    }
    func respond(_ key: String, _ status: Int, _ body: String) { lock.withLock { _responses[key] = (status, body) } }
    func hold(_ key: String) -> DispatchSemaphore {
        let s = DispatchSemaphore(value: 0)
        lock.withLock { _gates[key] = s; _arrived.remove(key) }
        return s
    }
    func arrived(_ key: String) -> Bool { lock.withLock { _arrived.contains(key) } }

    func handle(_ req: URLRequest) -> (HTTPURLResponse, Data) {
        let key = "\(req.httpMethod ?? "GET") \(req.url!.path)"
        let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let (gate, response) = lock.withLock { () -> (DispatchSemaphore?, (Int, String)?) in
            _log.append((key, body))
            let g = _gates.removeValue(forKey: key)
            if g != nil { _arrived.insert(key) }
            return (g, _responses[key])
        }
        let (status, text) = response ?? (404, #"{"error":"not found"}"#)
        gate?.wait()
        return (okResponse(req.url!, status: status), Data(text.utf8))
    }
}

@MainActor
private func waitFor(_ cond: () -> Bool) async throws {
    for _ in 0..<1000 where !cond() { try await Task.sleep(for: .milliseconds(5)) }
    try #require(cond())
}

@MainActor
private func facade(_ server: FacadeServer, id: String? = "clock-a",
                    clock: ManualClock = ManualClock()) async -> (ClockConfigModel, APIClient) {
    let client = stubbedClient(token: "t") { server.handle($0) }
    let m = ClockConfigModel(client: client, debounce: .milliseconds(600), sleep: clock.sleepFn)
    m.configure(client: client, deviceID: id)
    if id != nil { try? await waitFor { m.config.isLoaded || m.missing || m.config.loadError != nil } }
    return (m, client)
}

private func fixtureText(_ name: String) throws -> String {
    String(decoding: try fixture(name), as: UTF8.self)
}

@MainActor @Test func facadeModelLoadsTheRecordsConfig() async throws {
    let server = FacadeServer(config: try fixtureText("config_custom"))
    let (m, _) = await facade(server)
    #expect(m.isActive)
    #expect(m.config.isLoaded)
    #expect(m.config.draft == (try decodeFixture("config_custom")))
    #expect(server.log.contains("GET /v1/devices/clock-a/config"))
}

@MainActor @Test func facadeSaveIsAMergePutOfTheChangedFieldsOnly() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    m.config.draft.apps.calendar.tileLeadMinutes = 30
    m.config.draft.apps.focus.focusColor = "#123456"
    await m.config.saveNow()
    let body = try #require(server.bodies("PUT /v1/devices/clock-a/config").last)
    #expect(Set(body.keys) == ["apps"])
    let apps = try #require(body["apps"] as? [String: Any])
    #expect(Set(apps.keys) == ["calendar", "focus"])
    #expect(apps["calendar"] as? [String: Int] == ["tile_lead_minutes": 30])
    #expect(apps["focus"] as? [String: String] == ["focus_color": "#123456"])
    #expect(m.config.status == .saved)
    #expect(m.config.saveError == nil)
}

@MainActor @Test func busyFacadeSaveShowsWhyAndRefetches() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    server.respond("PUT /v1/devices/clock-a/config", 503, #"{"error":"another clock app order change is still running"}"#)
    server.respond("GET /v1/devices/clock-a/config", 200, try fixtureText("config_custom"))
    m.config.draft.apps.weather.forecastHours = 12
    await m.config.saveNow()
    guard case .rejected? = m.config.saveError else { Issue.record("expected a facade message"); return }
    if case .error = m.config.status {} else { Issue.record("expected an error status") }
    #expect(m.config.draft == (try decodeFixture("config_custom")))
    #expect(!m.config.hasUnsavedChanges)
    #expect(server.log.suffix(2) == ["PUT /v1/devices/clock-a/config", "GET /v1/devices/clock-a/config"])
}

@MainActor @Test func cutShortFacadeSaveShowsWhyAndRefetches() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    server.respond("PUT /v1/devices/clock-a/config", 502, #"{"error":"clock write failed"}"#)
    m.config.draft.apps.weather.on = false
    await m.config.saveNow()
    #expect(m.config.saveError == ClockConfigModel.facadeFailure(APIError.http(status: 502, body: "")))
    #expect(m.config.draft.apps.weather.on == true)
    #expect(!m.config.hasUnsavedChanges)
}

@MainActor @Test func failedRefetchKeepsTheEditForARetry() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    server.respond("PUT /v1/devices/clock-a/config", 503, #"{"error":"busy"}"#)
    server.respond("GET /v1/devices/clock-a/config", 503, #"{"error":"busy"}"#)
    m.config.draft.apps.weather.on = false
    await m.config.saveNow()
    guard case .rejected? = m.config.saveError else { Issue.record("expected a facade message"); return }
    #expect(m.config.hasUnsavedChanges)
}

@MainActor @Test func validationErrorKeepsTheServerMessage() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    server.respond("PUT /v1/devices/clock-a/config", 400, #"{"error":"forecast_hours must be 1..24"}"#)
    m.config.draft.apps.weather.forecastHours = 99
    await m.config.saveNow()
    guard case .server(let message)? = m.config.saveError else { Issue.record("expected a server error"); return }
    #expect(message.contains("forecast_hours"))
    #expect(m.config.hasUnsavedChanges)
}

@MainActor @Test func missingFacadeFallsBackToTheOldEndpoints() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    server.respond("GET /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    let (m, _) = await facade(server)
    #expect(m.missing)
    #expect(!m.isActive)
    let source = ConfigModel(initial: WeatherConfig(enabled: true),
                             load: { WeatherConfig(enabled: true, forecastHours: 6) }, save: { _ in })
    await source.load()
    let lens = ClockAppLens(source: source, clock: m, slice: \.weather)
    #expect(!lens.usesFacade)
    #expect(lens.draft.forecastHours == 6)
    lens.draft.forecastHours = 3
    #expect(source.draft.forecastHours == 3)
}

@MainActor @Test func noClockRecordMeansNoFacadeCalls() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server, id: nil)
    #expect(!m.isActive)
    await m.load()
    #expect(server.log.isEmpty)
}

@MainActor @Test func lensReadsAndWritesTheFacadeWhenActive() async throws {
    let server = FacadeServer(config: try fixtureText("config_custom"))
    let (m, _) = await facade(server)
    let source = ConfigModel(initial: WeatherConfig(),
                             load: { WeatherConfig(enabled: true, refreshMinutes: 15, forecastHours: 24) }, save: { _ in })
    await source.load()
    let lens = ClockAppLens(source: source, clock: m, slice: \.weather)
    #expect(lens.usesFacade && lens.isLoaded)
    #expect(lens.draft.enabled && lens.draft.refreshMinutes == 15)
    #expect(lens.draft.forecastHours == 6)
    lens.draft.forecastHours = 9
    #expect(m.config.draft.apps.weather.forecastHours == 9)
    #expect(source.draft.forecastHours == 24)
    #expect(!source.hasUnsavedChanges)
    #expect(m.config.draft.patch(from: try #require(m.config.applied))
            == ["apps": .object(["weather": .object(["forecast_hours": .int(9)])])])
}

@MainActor @Test func lensIsNotLoadedUntilBothSidesAre() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    let source = ConfigModel(initial: MeetingsConfig(), load: { throw APIError.http(status: 500, body: "") },
                             save: { _ in })
    let lens = ClockAppLens(source: source, clock: m, slice: \.calendar)
    await lens.load()
    #expect(!lens.isLoaded)
    #expect(lens.loadError != nil)
}

@MainActor @Test func reconfigureDropsALoadForTheOldRecord() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let client = stubbedClient(token: "t") { server.handle($0) }
    let m = ClockConfigModel(client: client, debounce: .milliseconds(600), sleep: ManualClock().sleepFn)
    server.respond("GET /v1/devices/clock-a/config", 404, #"{"error":"gone"}"#)
    server.respond("GET /v1/devices/clock-b/config", 200, try fixtureText("config_custom"))
    let gate = server.hold("GET /v1/devices/clock-a/config")
    m.configure(client: client, deviceID: "clock-a")
    try await waitFor { server.arrived("GET /v1/devices/clock-a/config") }
    m.configure(client: client, deviceID: "clock-b")
    try await waitFor { m.config.isLoaded }
    gate.signal()
    try await Task.sleep(for: .milliseconds(100))
    #expect(!m.missing)
    #expect(m.isActive)
    #expect(m.deviceID == "clock-b")
    #expect(m.config.draft == (try decodeFixture("config_custom")))
}

@MainActor @Test func olderLoadLandingLastIsDropped() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    let gate = server.hold("GET /v1/devices/clock-a/config")
    let first = Task { await m.config.load() }
    try await waitFor { server.arrived("GET /v1/devices/clock-a/config") }
    server.respond("GET /v1/devices/clock-a/config", 200, try fixtureText("config_custom"))
    await m.config.load()
    #expect(m.config.draft == (try decodeFixture("config_custom")))
    server.respond("GET /v1/devices/clock-a/config", 200, try fixtureText("config_default"))
    gate.signal()
    await first.value
    #expect(m.config.draft == (try decodeFixture("config_custom")))
}

@MainActor @Test func loadInFlightDuringASaveDoesNotUndoIt() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    let gate = server.hold("GET /v1/devices/clock-a/config")
    let load = Task { await m.config.load() }
    try await waitFor { server.arrived("GET /v1/devices/clock-a/config") }
    m.config.draft.apps.calendar.on = false
    await m.config.saveNow()
    gate.signal()
    await load.value
    #expect(m.config.draft.apps.calendar.on == false)
    #expect(m.config.applied?.apps.calendar.on == false)
}

@MainActor @Test func savedHookFiresWithTheSlicesThatChanged() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    var seen: [(ClockConfig, ClockConfig?)] = []
    m.onSaved = { seen.append(($0, $1)) }
    m.config.draft.apps.weather.air = false
    await m.config.saveNow()
    let (saved, previous) = try #require(seen.first)
    #expect(saved.apps.weather.air == false)
    #expect(previous?.apps.weather.air == true)
}

@MainActor @Test func rotationWriteSendsNGSemantics() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    try await m.writeRotation(AppsUpdate(order: ["date", "time"], disabled: ["hum"]))
    let body = try #require(server.bodies("PUT /v1/devices/clock-a/config").last)
    #expect(Set(body.keys) == ["rotation"])
    let rotation = try #require(body["rotation"] as? [String: [String]])
    #expect(rotation == ["order": ["date", "time"], "disabled": ["hum"]])
}

@MainActor @Test func rotationWriteOnAMissingRecordMarksItMissing() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    server.respond("PUT /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    await #expect(throws: (any Error).self) { try await m.writeRotation(AppsUpdate(order: ["time"])) }
    #expect(m.missing && !m.isActive)
}

private let legacyPomodoro = ##"{"enabled":true,"focus_minutes":25,"short_break_minutes":5,"long_break_minutes":15,"rounds_before_long_break":4,"auto_start_next":false,"sound":true,"focus_color":"#FF0000","break_color":"#00FF00","max_session_minutes":480}"##
private let legacyWeather = #"{"enabled":true,"rotate_in_apps":true,"air_tile":true}"#

@MainActor
private func linked(_ server: FacadeServer, probeBackoff: TimeInterval = 300,
                    clock: ManualClock = ManualClock()) async throws -> (ClockConfigModel, SettingsModels, APIClient) {
    server.respond("GET /v1/pomodoro/config", 200, legacyPomodoro)
    server.respond("PUT /v1/pomodoro/config", 200, "")
    server.respond("GET /v1/weather/config", 200, legacyWeather)
    server.respond("PUT /v1/weather/config", 200, "")
    server.respond("GET /v1/meetings/config", 200, #"{"enabled":false,"tile_lead_minutes":15,"popup_lead_minutes":0}"#)
    server.respond("PUT /v1/meetings/config", 200, "")
    let client = stubbedClient(token: "t") { server.handle($0) }
    let settings = SettingsModels(client: client, envStore: EnvFileStore(path: URL(fileURLWithPath: "/nonexistent/producer.env")))
    let m = ClockConfigModel(client: client, debounce: .milliseconds(600), sleep: clock.sleepFn,
                             probeBackoff: probeBackoff)
    m.legacy = settings
    m.configure(client: client, deviceID: "clock-a")
    try await waitFor { m.config.isLoaded || m.missing }
    await settings.pomodoro.load()
    await settings.weather.load()
    await settings.meetings.load()
    try #require(settings.pomodoro.isLoaded && settings.weather.isLoaded && settings.meetings.isLoaded)
    return (m, settings, client)
}

private func count(_ server: FacadeServer, _ key: String) -> Int { server.log.filter { $0 == key }.count }

@MainActor @Test func facadeSaveUpdatesAnIdleLegacyModelInPlace() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    server.respond("GET /v1/pomodoro/config", 500, #"{"error":"boom"}"#)
    m.config.draft.apps.focus.focusColor = "#123456"
    await m.config.saveNow()
    #expect(settings.pomodoro.draft.focusColor == "#123456")
    #expect(settings.pomodoro.applied?.focusColor == "#123456")
    #expect(!settings.pomodoro.hasUnsavedChanges)
    #expect(count(server, "PUT /v1/pomodoro/config") == 0)
}

@MainActor @Test func pendingLegacyEditCarriesTheFacadeValue() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    settings.pomodoro.draft.focusMinutes = 50
    m.config.draft.apps.focus.focusColor = "#123456"
    await m.config.saveNow()
    await settings.pomodoro.saveNow()
    let body = try #require(server.bodies("PUT /v1/pomodoro/config").last)
    #expect(body["focus_color"] as? String == "#123456")
    #expect(body["focus_minutes"] as? Int == 50)
}

@MainActor @Test(.timeLimit(.minutes(1))) func inFlightLegacySaveIsResentWithTheFacadeValue() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    let gate = server.hold("PUT /v1/pomodoro/config")
    settings.pomodoro.draft.focusMinutes = 50
    let legacySave = Task { await settings.pomodoro.saveNow() }
    try await waitFor { server.arrived("PUT /v1/pomodoro/config") }
    m.config.draft.apps.focus.focusColor = "#123456"
    await m.config.saveNow()
    gate.signal()
    await legacySave.value
    try await waitFor { count(server, "PUT /v1/pomodoro/config") == 2 }
    let bodies = server.bodies("PUT /v1/pomodoro/config")
    #expect(bodies.first?["focus_color"] as? String == "#FF0000")
    #expect(bodies.last?["focus_color"] as? String == "#123456")
    #expect(bodies.last?["focus_minutes"] as? Int == 50)
}

@MainActor @Test(.timeLimit(.minutes(1))) func legacySaveDuringAFacadeSaveIsResentWithTheFacadeValue() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    let gate = server.hold("PUT /v1/devices/clock-a/config")
    m.config.draft.apps.focus.focusColor = "#123456"
    let facadeSave = Task { await m.config.saveNow() }
    try await waitFor { server.arrived("PUT /v1/devices/clock-a/config") }
    settings.pomodoro.draft.focusMinutes = 50
    await settings.pomodoro.saveNow()
    gate.signal()
    await facadeSave.value
    try await waitFor { count(server, "PUT /v1/pomodoro/config") == 2 }
    let last = try #require(server.bodies("PUT /v1/pomodoro/config").last)
    #expect(last["focus_color"] as? String == "#123456")
    #expect(last["focus_minutes"] as? Int == 50)
}

@MainActor @Test(.timeLimit(.minutes(1))) func recoveredSaveKeepsEditsMadeWhileItWasInFlight() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let clock = ManualClock()
    let (m, _) = await facade(server, clock: clock)
    server.respond("PUT /v1/devices/clock-a/config", 503, #"{"error":"busy"}"#)
    let gate = server.hold("PUT /v1/devices/clock-a/config")
    m.config.draft.apps.weather.forecastHours = 12
    let save = Task { await m.config.saveNow() }
    try await waitFor { server.arrived("PUT /v1/devices/clock-a/config") }
    m.config.draft.apps.calendar.tileLeadMinutes = 30
    server.respond("GET /v1/devices/clock-a/config", 200, try fixtureText("config_custom"))
    gate.signal()
    await save.value
    let custom = try decodeFixture("config_custom")
    guard case .rejected? = m.config.saveError else { Issue.record("expected a facade message"); return }
    #expect(m.config.applied == custom)
    #expect(m.config.draft.apps.calendar.tileLeadMinutes == 30)
    #expect(m.config.draft.apps.weather == custom.apps.weather)
    #expect(m.config.draft.patch(from: custom) == ["apps": .object(["calendar": .object(["tile_lead_minutes": .int(30)])])])
    #expect(m.config.hasPendingSave)
    await clock.advance(by: .milliseconds(600))
    try await waitFor { !m.config.isSaving }
    #expect(count(server, "PUT /v1/devices/clock-a/config") == 2)
    #expect(server.bodies("PUT /v1/devices/clock-a/config").last?["apps"] as? [String: [String: Int]]
            == ["calendar": ["tile_lead_minutes": 30]])
    #expect(m.config.draft == custom)
    #expect(!m.config.hasUnsavedChanges && !m.config.hasPendingSave)
    await clock.advance(by: .seconds(5))
    #expect(count(server, "PUT /v1/devices/clock-a/config") == 2)
}

@Test func rebaseReplacesWholeValuesInsteadOfMergingThem() throws {
    let sent = try decodeFixture("config_custom")
    var draft = sent
    draft.apps.weather.iconIds = [:]
    var current = sent
    current.apps.focus.focusColor = "#000000"
    let rebased = draft.rebased(onto: current, from: sent)
    #expect(rebased.apps.weather.iconIds.isEmpty)
    #expect(rebased.apps.focus.focusColor == "#000000")
}

@MainActor @Test(.timeLimit(.minutes(1))) func facadeSaveOnAMissingRecordReplaysTheEditThroughTheOldEndpoint() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    server.respond("PUT /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    m.config.draft.apps.weather.air = false
    await m.config.saveNow()
    #expect(m.missing && !m.isActive)
    #expect(m.status == .idle)
    let lens = ClockAppLens(source: settings.weather, clock: m, slice: \.weather)
    #expect(lens.draft.airTile == false)
    try await waitFor { count(server, "PUT /v1/weather/config") == 1 }
    #expect(server.bodies("PUT /v1/weather/config").last?["air_tile"] as? Bool == false)
}

@MainActor @Test func paneReloadRecoversAFacadeThatCameBack() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    server.respond("GET /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    let (m, settings, _) = try await linked(server)
    #expect(m.missing)
    server.respond("GET /v1/devices/clock-a/config", 200, try fixtureText("config_custom"))
    await ClockAppLens(source: settings.weather, clock: m, slice: \.weather).load()
    #expect(!m.missing && m.isActive)
    #expect(m.config.draft == (try decodeFixture("config_custom")))
}

@MainActor @Test func deviceRefreshRetriesAMissingFacadeAfterTheBackoff() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    server.respond("GET /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    let (m, _, client) = try await linked(server, probeBackoff: 0)
    #expect(m.missing)
    server.respond("GET /v1/devices/clock-a/config", 200, try fixtureText("config_default"))
    m.configure(client: client, deviceID: "clock-a")
    try await waitFor { !m.missing }
    #expect(m.isActive)
}

@MainActor @Test func legacySaveRefreshesTheFacade() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    settings.onClockSourceSaved = { Task { await m.load() } }
    let gets = count(server, "GET /v1/devices/clock-a/config")
    settings.pomodoro.draft.focusMinutes = 50
    await settings.pomodoro.saveNow()
    try await waitFor { count(server, "GET /v1/devices/clock-a/config") == gets + 1 }
}

@MainActor @Test func recordFlapSendsAPendingFacadeEdit() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, client) = await facade(server)
    m.config.draft.apps.calendar.on = false
    m.config.scheduleSave()
    m.configure(client: client, deviceID: nil)
    try await waitFor { count(server, "PUT /v1/devices/clock-a/config") == 1 }
    #expect(server.bodies("PUT /v1/devices/clock-a/config").last?["apps"] as? [String: [String: Bool]]
            == ["calendar": ["on": false]])
}

@Test func expandedClockGroupsFollowTheRecordID() {
    let tree = SettingsTree(devices: [SettingsDevice(id: "clock-05ffb8", kind: .clock, name: "Clock", state: .ready)])
    let moved = tree.expanded(["clock", "clock/apps", "elsewhere"], revealing: .app(.general))
    #expect(moved == ["clock-05ffb8", "clock-05ffb8/apps", "elsewhere"])
}

private final class LoadGate: @unchecked Sendable {
    private let lock = NSLock()
    private var calls = 0
    let release = DispatchSemaphore(value: 0)
    var count: Int { lock.withLock { calls } }
    func next() -> Int { lock.withLock { calls += 1; return calls } }
}

@MainActor @Test(.timeLimit(.minutes(1))) func olderLoadStillAppliesWhenANewerOneFailsBeforeAnyData() async throws {
    let gate = LoadGate()
    let m = ConfigModel<Int>(initial: 0, load: {
        if gate.next() == 1 {
            await withCheckedContinuation { c in DispatchQueue.global().async { gate.release.wait(); c.resume() } }
            return 7
        }
        throw APIError.http(status: 500, body: "")
    }, save: { _ in })
    let first = Task { await m.load() }
    try await waitFor { gate.count == 1 }
    await m.load()
    gate.release.signal()
    await first.value
    #expect(m.isLoaded)
    #expect(m.draft == 7)
}

@MainActor @Test func facadeSaveKeepsAPendingSharedFieldEdit() async throws {
    let server = FacadeServer(config: try fixtureText("config_custom"))
    let (m, settings, _) = try await linked(server)
    settings.meetings.draft.enabled = true
    settings.meetings.scheduleSave()
    m.config.draft.apps.calendar.tileLeadMinutes = 30
    await m.config.saveNow()
    #expect(settings.meetings.draft.enabled == true)
    #expect(settings.meetings.draft.tileLeadMinutes == 30)
    #expect(settings.meetings.hasUnsavedChanges)
    await settings.meetings.saveNow()
    let body = try #require(server.bodies("PUT /v1/meetings/config").last)
    #expect(body["enabled"] as? Bool == true)
    #expect(body["tile_lead_minutes"] as? Int == 30)
}

@MainActor @Test(.timeLimit(.minutes(1))) func facadeSaveKeepsAnInFlightSharedFieldEdit() async throws {
    let server = FacadeServer(config: try fixtureText("config_custom"))
    let (m, settings, _) = try await linked(server)
    let gate = server.hold("PUT /v1/meetings/config")
    settings.meetings.draft.enabled = true
    let legacySave = Task { await settings.meetings.saveNow() }
    try await waitFor { server.arrived("PUT /v1/meetings/config") }
    m.config.draft.apps.calendar.tileLeadMinutes = 30
    await m.config.saveNow()
    gate.signal()
    await legacySave.value
    try await waitFor { count(server, "PUT /v1/meetings/config") == 2 }
    let last = try #require(server.bodies("PUT /v1/meetings/config").last)
    #expect(last["enabled"] as? Bool == true)
    #expect(last["tile_lead_minutes"] as? Int == 30)
}

@MainActor @Test func replayKeepsAPendingSharedFieldEdit() async throws {
    let server = FacadeServer(config: try fixtureText("config_custom"))
    let (m, settings, _) = try await linked(server)
    server.respond("PUT /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    settings.meetings.draft.enabled = true
    settings.meetings.scheduleSave()
    m.config.draft.apps.calendar.tileLeadMinutes = 30
    await m.config.saveNow()
    #expect(settings.meetings.draft.enabled == true)
    #expect(settings.meetings.draft.tileLeadMinutes == 30)
    await settings.meetings.saveNow()
    let body = try #require(server.bodies("PUT /v1/meetings/config").last)
    #expect(body["enabled"] as? Bool == true)
    #expect(body["tile_lead_minutes"] as? Int == 30)
}

@MainActor @Test(.timeLimit(.minutes(1))) func legacyLoadInFlightAcrossAFacadeSaveIsDropped() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    let gate = server.hold("GET /v1/pomodoro/config")
    let staleLoad = Task { await settings.pomodoro.load() }
    try await waitFor { server.arrived("GET /v1/pomodoro/config") }
    m.config.draft.apps.focus.focusColor = "#123456"
    await m.config.saveNow()
    gate.signal()
    await staleLoad.value
    #expect(settings.pomodoro.draft.focusColor == "#123456")
    #expect(settings.pomodoro.applied?.focusColor == "#123456")
}

@MainActor @Test(.timeLimit(.minutes(1))) func replayForwardsEditsMadeWhileThePutWasInFlight() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    server.respond("PUT /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    let gate = server.hold("PUT /v1/devices/clock-a/config")
    m.config.draft.apps.weather.air = false
    let save = Task { await m.config.saveNow() }
    try await waitFor { server.arrived("PUT /v1/devices/clock-a/config") }
    m.config.draft.apps.weather.forecastHours = 12
    gate.signal()
    await save.value
    #expect(settings.weather.draft.airTile == false)
    #expect(settings.weather.draft.forecastHours == 12)
}

@MainActor @Test func adoptSkipsModelsReplacedByAServerChange() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (_, settings, _) = try await linked(server)
    let mark = settings.clockMark()
    let other = FacadeServer(config: try fixtureText("config_default"))
    settings.configure(client: stubbedClient(token: "u") { other.handle($0) })
    var saved = try decodeFixture("config_default")
    saved.apps.focus.focusColor = "#123456"
    settings.adopt(saved, previous: try decodeFixture("config_default"), since: mark)
    #expect(settings.pomodoro.draft.focusColor != "#123456")
}

@MainActor @Test func probeNeedsAFreshGetToReenableTheFacade() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, _) = await facade(server)
    m.config.draft.apps.calendar.on = false
    m.config.scheduleSave()
    server.respond("PUT /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    _ = try? await m.writeRotation(AppsUpdate(order: ["time"]))
    #expect(m.missing)
    let gets = count(server, "GET /v1/devices/clock-a/config")
    await m.load()
    #expect(m.missing)
    #expect(count(server, "GET /v1/devices/clock-a/config") == gets)
}

@MainActor @Test(.timeLimit(.minutes(1))) func replayDropsTheFacadeSaveScheduledDuringThePut() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let clock = ManualClock()
    let (m, settings, _) = try await linked(server, clock: clock)
    server.respond("PUT /v1/devices/clock-a/config", 404, #"{"error":"not found"}"#)
    let gate = server.hold("PUT /v1/devices/clock-a/config")
    m.config.draft.apps.weather.air = false
    let save = Task { await m.config.saveNow() }
    try await waitFor { server.arrived("PUT /v1/devices/clock-a/config") }
    m.config.draft.apps.weather.forecastHours = 12
    m.config.scheduleSave()
    gate.signal()
    await save.value
    #expect(settings.weather.draft.forecastHours == 12)
    settings.weather.draft.forecastHours = 6
    await clock.advance(by: .milliseconds(600))
    #expect(count(server, "PUT /v1/devices/clock-a/config") == 1)
    #expect(settings.weather.draft.forecastHours == 6)
}

@MainActor @Test(.timeLimit(.minutes(1))) func facadeSaveClearsAStaleLegacyLoadError() async throws {
    let server = FacadeServer(config: try fixtureText("config_default"))
    let (m, settings, _) = try await linked(server)
    server.respond("GET /v1/pomodoro/config", 500, #"{"error":"boom"}"#)
    await settings.pomodoro.load()
    try #require(settings.pomodoro.loadError != nil && settings.pomodoro.isLoaded)
    server.respond("GET /v1/pomodoro/config", 200, legacyPomodoro)
    let gate = server.hold("GET /v1/pomodoro/config")
    let retry = Task { await settings.pomodoro.load() }
    try await waitFor { server.arrived("GET /v1/pomodoro/config") }
    m.config.draft.apps.focus.focusColor = "#123456"
    await m.config.saveNow()
    gate.signal()
    await retry.value
    #expect(settings.pomodoro.loadError == nil)
    #expect(ClockAppLens(source: settings.pomodoro, clock: m, slice: \.focus).loadError == nil)
}

private func checkFieldDiff<S: ClockAppSlice>(_ base: S, other: S, blank: S.Source,
                                              _ fields: [(name: String, change: (inout S) -> Void)]) {
    var fromBase = blank
    base.apply(to: &fromBase)
    var fromOther = blank
    other.apply(to: &fromOther)
    var all = base
    for (name, change) in fields {
        var sent = base
        change(&sent)
        #expect(sent != base, "\(name) leaves the slice unchanged")
        var written = fromBase
        sent.apply(to: &written, changedFrom: base)
        #expect(written != fromBase, "\(name) is not written")
        var kept = fromOther
        sent.apply(to: &kept, changedFrom: base)
        var only = other
        only.take(from: fromOther)
        change(&only)
        var want = fromOther
        only.apply(to: &want)
        #expect(kept == want, "\(name) writes more than itself")
        change(&all)
    }
    var got = fromBase
    all.apply(to: &got, changedFrom: base)
    var want = fromBase
    all.apply(to: &want)
    #expect(got == want, "apply(to:) maps a field apply(to:changedFrom:) misses")
}

@MainActor @Test func everyFacadeFieldIsWrittenAloneWhenItChanged() {
    checkFieldDiff(ClockConfig.Agents(), other: ClockConfig.Agents(usageCards: false, usagePerModel: false),
                   blank: UsageConfig(), [
        ("usageCards", { $0.usageCards = false }),
        ("usagePerModel", { $0.usagePerModel = false }),
    ])
    checkFieldDiff(ClockConfig.Focus(), other: ClockConfig.Focus(focusColor: "#111111", breakColor: "#222222"),
                   blank: SettingsModels.defaultPomoConfig, [
        ("focusColor", { $0.focusColor = "#AAAAAA" }),
        ("breakColor", { $0.breakColor = "#BBBBBB" }),
    ])
    let otherWeather = ClockConfig.Weather(
        on: false, nativeIcon: true, forecast: false, forecastHours: 6, air: false, moon: false, overlay: false,
        popups: ClockConfig.Popups(onChange: false, sun: false, severe: false, nativeIcons: true,
                                   intervalMinutes: 30, durationSeconds: 8),
        iconIds: ["clear": "1"])
    checkFieldDiff(ClockConfig.Weather(), other: otherWeather, blank: WeatherConfig(), [
        ("on", { $0.on = false }),
        ("nativeIcon", { $0.nativeIcon = true }),
        ("forecast", { $0.forecast = false }),
        ("forecastHours", { $0.forecastHours = 12 }),
        ("air", { $0.air = false }),
        ("moon", { $0.moon = false }),
        ("overlay", { $0.overlay = false }),
        ("popups.onChange", { $0.popups.onChange = false }),
        ("popups.sun", { $0.popups.sun = false }),
        ("popups.severe", { $0.popups.severe = false }),
        ("popups.nativeIcons", { $0.popups.nativeIcons = true }),
        ("popups.intervalMinutes", { $0.popups.intervalMinutes = 60 }),
        ("popups.durationSeconds", { $0.popups.durationSeconds = 15 }),
        ("iconIds", { $0.iconIds = ["rain": "9"] }),
    ])
    checkFieldDiff(ClockConfig.Calendar(), other: ClockConfig.Calendar(on: false, tileLeadMinutes: 15, popupLeadMinutes: 0),
                   blank: MeetingsConfig(), [
        ("on", { $0.on = false }),
        ("tileLeadMinutes", { $0.tileLeadMinutes = 30 }),
        ("popupLeadMinutes", { $0.popupLeadMinutes = 5 }),
    ])
}

@Test func iconOverridesAreWrittenAsAWholeValue() {
    let before = ClockConfig.Weather(iconIds: ["clear": "1", "rain": "2"])
    var sent = before
    sent.iconIds = ["clear": "1"]
    var source = WeatherConfig(iconIds: ["clear": "1", "rain": "2", "snow": "3"])
    sent.apply(to: &source, changedFrom: before)
    #expect(source.iconIds == ["clear": "1"])
}

@Test func aFieldChangedAndChangedBackIsNotWritten() {
    let before = ClockConfig.Weather(iconIds: ["clear": "1"])
    var sent = before
    sent.popups.sun = false
    sent.iconIds = [:]
    sent.air = false
    sent.popups.sun = true
    sent.iconIds = ["clear": "1"]
    var source = WeatherConfig(sunPopups: false, iconIds: ["rain": "2"], airTile: true)
    sent.apply(to: &source, changedFrom: before)
    #expect(source.sunPopups == false)
    #expect(source.iconIds == ["rain": "2"])
    #expect(source.airTile == false)
}
