import Testing
import Foundation
import Network
@testable import EmberKit

private let serverMenuCallBudget: TimeInterval = 8
private let serverDiscoverBudget: TimeInterval = 8
private let serverReminderFireBudget: TimeInterval = 10
private let serverWriteTimeout: TimeInterval = 30
private let serverClockWriteBudget: TimeInterval = 25

@Test func serverBudgetStaysShortSoADeadServerShowsFast() {
    #expect(RequestBudget.server.requestTimeout == 5)
    #expect(RequestBudget.server.resourceTimeout == 10)
}

@Test func clockBudgetOutlastsOneClockCall() {
    #expect(RequestBudget.clock.requestTimeout > serverMenuCallBudget)
    #expect(RequestBudget.clock.requestTimeout > serverDiscoverBudget)
    #expect(RequestBudget.clock.resourceTimeout >= RequestBudget.clock.requestTimeout)
}

@Test func clockLongBudgetOutlastsTheServersWriteTimeout() {
    #expect(RequestBudget.clockLong.requestTimeout > serverWriteTimeout)
    #expect(RequestBudget.clockLong.requestTimeout > serverClockWriteBudget)
    #expect(RequestBudget.clockLong.requestTimeout > serverReminderFireBudget)
    #expect(RequestBudget.clockLong.resourceTimeout >= RequestBudget.clockLong.requestTimeout)
}

@Test(arguments: RequestBudget.allCases)
func eachBudgetHasASessionWithItsTimeouts(budget: RequestBudget) {
    let session = APIClient.session(for: budget)
    #expect(session !== URLSession.shared)
    #expect(session.configuration.timeoutIntervalForRequest == budget.requestTimeout)
    #expect(session.configuration.timeoutIntervalForResource == budget.resourceTimeout)
}

@Test(arguments: RequestBudget.allCases)
func eachBudgetsSessionSkipsTheURLCache(budget: RequestBudget) {
    let config = APIClient.session(for: budget).configuration
    #expect(config.urlCache == nil)
    #expect(config.requestCachePolicy == .reloadIgnoringLocalCacheData)
}

@Test(arguments: RequestBudget.allCases)
func defaultClientRunsEachBudgetOnItsSession(budget: RequestBudget) {
    let client = APIClient(baseURL: URL(string: "http://example.local"), token: nil)
    #expect(client.sessions(budget) === APIClient.session(for: budget))
}

private let budgetHeader = "X-Test-Budget"

private final class BudgetLog: @unchecked Sendable {
    private let lock = NSLock()
    private var entries: [String: String] = [:]
    func add(_ req: URLRequest) {
        let key = "\(req.httpMethod ?? "") \(req.url?.path ?? "")"
        let budget = req.value(forHTTPHeaderField: budgetHeader) ?? "none"
        lock.withLock { entries[key] = budget }
    }
    subscript(_ key: String) -> String? { lock.withLock { entries[key] } }
}

private func budgetRecordingClient(body: @escaping @Sendable (URLRequest) -> String = { _ in "{}" })
    -> (APIClient, BudgetLog) {
    let log = BudgetLog()
    let host = "stub-\(UUID().uuidString.lowercased()).local"
    StubURLProtocol.register(host: host) { req in
        log.add(req)
        return (okResponse(req.url!), Data(body(req).utf8))
    }
    var sessions: [RequestBudget: URLSession] = [:]
    for budget in RequestBudget.allCases {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubURLProtocol.self]
        config.httpAdditionalHeaders = [budgetHeader: "\(budget)"]
        sessions[budget] = URLSession(configuration: config)
    }
    let pick = sessions
    let client = APIClient(baseURL: URL(string: "http://\(host)"), token: nil,
                           sessions: { pick[$0]! }, pathStatus: { .satisfied })
    return (client, log)
}

@Test func everyClockProxyCallRunsOnAClockSession() async {
    let (client, log) = budgetRecordingClient { req in
        switch req.url?.path {
        case "/v1/device/apps": "[]"
        case "/v1/device/screen": #"{"width":32,"height":8,"pixels":[]}"#
        default: "{}"
        }
    }
    let device = DeviceService(client: client)
    _ = try? await device.settings()
    _ = try? await device.update(patch: [:])
    _ = try? await device.display()
    _ = try? await device.updateDisplay(DeviceDisplay())
    _ = try? await device.setDisplayPower(false)
    _ = try? await device.playTestChime()
    _ = try? await device.stopAudio()
    _ = try? await device.melodies()
    _ = try? await device.apps()
    _ = try? await device.updateApps(AppsUpdate(order: [], disabled: []))
    _ = try? await device.capabilities()
    _ = try? await device.stats()
    _ = try? await device.sensors()
    _ = try? await device.updateSensors(SensorCalibration(tempOffset: 0, humOffset: 0))
    _ = try? await device.screen()
    _ = try? await device.reboot()
    _ = try? await device.dismiss()
    _ = try? await device.nextApp()
    _ = try? await device.previousApp()
    _ = try? await device.discover()
    _ = try? await device.updateButtons(enabled: true)
    _ = try? await device.config()
    _ = try? await device.setConfig(baseURL: "http://192.168.0.66")

    let expected: [String: RequestBudget] = [
        "GET /v1/device/settings": .clockLong,
        "PUT /v1/device/settings": .clockLong,
        "GET /v1/device/display": .clock,
        "PUT /v1/device/display": .clock,
        "PUT /v1/device/display/power": .clock,
        "POST /v1/device/audio/test": .clock,
        "POST /v1/device/audio/stop": .clock,
        "GET /v1/device/audio/melodies": .clock,
        "GET /v1/device/apps": .clock,
        "PUT /v1/device/apps": .clock,
        "GET /v1/device/capabilities": .clock,
        "GET /v1/device/stats": .clock,
        "GET /v1/device/sensors": .clock,
        "PUT /v1/device/sensors": .clockLong,
        "GET /v1/device/screen": .clock,
        "POST /v1/device/reboot": .clock,
        "POST /v1/device/notify/dismiss": .clock,
        "POST /v1/device/app/next": .clock,
        "POST /v1/device/app/previous": .clock,
        "GET /v1/device/discover": .clock,
        "PUT /v1/device/buttons": .clockLong,
        "GET /v1/device/buttons": .clock,
        "GET /v1/device/config": .server,
        "PUT /v1/device/config": .server,
    ]
    for (call, budget) in expected {
        #expect(log[call] == "\(budget)", "\(call)")
    }
}

@Test func plainServerCallsKeepTheServerSession() async throws {
    let (client, log) = budgetRecordingClient { _ in #"{"sessions":[]}"# }
    let _: Snapshot = try await client.get("/state")
    try await client.send("GET", "/healthz")
    #expect(log["GET /state"] == "server")
    #expect(log["GET /healthz"] == "server")
}

@Test func reminderFireRunsOnTheLongClockSession() async throws {
    let (client, log) = budgetRecordingClient { _ in "" }
    try await client.postIdempotent("/v1/reminders/fire", body: ["x": 1], key: "k")
    #expect(log["POST /v1/reminders/fire"] == "clockLong")
}

@Test func timeoutIsItsOwnError() async {
    let client = stubbedClient { _ in throw URLError(.timedOut) }
    await #expect(throws: APIError.timedOut) { try await client.send("GET", "/v1/device/stats", budget: .clock) }
}

@Test func timedOutIdempotentPostIsNotNotSent() async {
    let client = stubbedClient { _ in throw URLError(.timedOut) }
    await #expect(throws: APIError.timedOut) {
        try await client.postIdempotent("/v1/reminders/fire", body: ["x": 1], key: "k")
    }
}

@Test func droppedConnectionIsATimeoutOnlyForTheLongClockBudget() {
    let lost = URLError(.networkConnectionLost)
    #expect(APIClient.classify(lost, budget: .clockLong, host: "192.168.0.2", pathStatus: .satisfied) == .timedOut)
    for budget in [RequestBudget.server, .clock] {
        let e = APIClient.classify(lost, budget: budget, host: "192.168.0.2", pathStatus: .satisfied)
        guard case .transport = e else { Issue.record("\(budget): got \(e)"); continue }
    }
}

@Test func droppedLongClockRequestReadsNotResponding() async {
    let client = stubbedClient { _ in throw URLError(.networkConnectionLost) }
    await #expect(throws: APIError.timedOut) {
        try await client.put("/v1/device/sensors", body: ["temp_offset": 0], budget: .clockLong)
    }
}

@Test func timeoutReachesEveryReport() {
    #expect(FeedError(APIError.timedOut) == .timedOut)
    #expect(FeedError(URLError(.timedOut)) == .timedOut)
    #expect(FeedError(URLError(.cannotConnectToHost)) == .offline)
    #expect(FeedError(APIError.transport("x")) == .offline)
    #expect(FeedError.timedOut.isUnreachable)
    #expect(String(localized: FeedError.timedOut.message) == "The server didn't answer in time")
    #expect(APIError.timedOut.errorDescription == "The server didn't answer in time")
    #expect(ConnectionProbe.result(for: APIError.timedOut) == .timedOut)
    #expect(ConnectionProbe.result(for: APIError.transport("x")) == .unreachable)
    #expect(LocalNetworkProbe.serverOutcome(for: APIError.timedOut) == .unreachable)
}

@Test func offlineHeaderAndSubtitleNameATimeout() {
    let since = Date(timeIntervalSince1970: 0)
    let utc = TimeZone(identifier: "UTC")!
    let en = Locale(identifier: "en_US")
    let header = MenuRows.header(connection: .offline(since: since), hasEverLoaded: true, winning: nil,
                                 offlineReason: .timedOut, locale: en, timeZone: utc)
    #expect(String(localized: header.title).hasPrefix("Offline — server not responding since"))
    let subtitle = ConnectionHealth.offline(since: since).subtitle(serverHost: "h", offlineReason: .timedOut,
                                                                   locale: en, timeZone: utc)
    #expect(String(localized: subtitle).hasPrefix("Offline: not responding since"))
    let plain = MenuRows.header(connection: .offline(since: since), hasEverLoaded: true, winning: nil,
                                offlineReason: .offline, locale: en, timeZone: utc)
    #expect(String(localized: plain.title).hasPrefix("Offline — server unreachable since"))
}

@Test func refusalUnderAClockBudgetIsStillDenied() async {
    let refused = URLError(.notConnectedToInternet,
                           userInfo: ["_kCFStreamErrorDomainKey": 1, "_kCFStreamErrorCodeKey": 50])
    let client = stubbedClient { _ in throw refused }
    await #expect(throws: APIError.localNetworkDenied) {
        try await client.send("POST", "/v1/device/reboot", budget: .clock)
    }
    await #expect(throws: APIError.localNetworkDenied) {
        try await client.put("/v1/device/sensors", body: ["temp_offset": 0], budget: .clockLong)
    }
}

@Test(arguments: RequestBudget.allCases)
func classifierPutsTheRefusalFirst(budget: RequestBudget) {
    let noAuth = NWError.dns(LocalNetworkDenial.dnsNoAuth)
    #expect(APIClient.classify(noAuth, budget: budget, host: "192.168.0.2", pathStatus: .satisfied)
        == .localNetworkDenied)
    #expect(APIClient.classify(URLError(.timedOut), budget: budget, host: "192.168.0.2", pathStatus: .satisfied)
        == .timedOut)
}

private let serverELFReadDeadline: TimeInterval = 300

@Test func transferBudgetOutlastsTheServersELFReadDeadline() {
    #expect(RequestBudget.transfer.resourceTimeout > serverELFReadDeadline)
    #expect(RequestBudget.transfer.requestTimeout > serverWriteTimeout)
}

@Test func firmwareTransfersRunOnTheTransferSession() async throws {
    let (client, log) = budgetRecordingClient { req in
        req.url?.path == "/v1/firmware" ? #"{"build":"a","channel":"test","elf":false,"idf_ver":"v","project":"cinder","sha256":"s","size":1,"uploaded_at":"2026-10-06T12:00:00Z","version":"0.9.14"}"# : "{}"
    }
    let svc = KnobService(client: client)
    _ = try await svc.uploadFirmware(Data([1]), channel: "test")
    try await svc.uploadELF(version: "0.9.14", Data([1]))
    _ = try await svc.firmwareELF(version: "0.9.14")
    _ = try await svc.firmwareELF(build: "a1b2c3d4")
    #expect(log["POST /v1/firmware"] == "transfer")
    #expect(log["PUT /v1/firmware/0.9.14/elf"] == "transfer")
    #expect(log["GET /v1/firmware/0.9.14/elf"] == "transfer")
    #expect(log["GET /v1/firmware/by-build/a1b2c3d4/elf"] == "transfer")
}
