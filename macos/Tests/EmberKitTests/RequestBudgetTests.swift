import Testing
import Foundation
import Network
@testable import EmberKit

// MARK: Budgets against the server's own (cmd/ember/clock_access.go, main.go)

/// `menuCallTimeout`: one menu-class clock call.
private let serverMenuCallBudget: TimeInterval = 8
/// Discovery: mDNS browse 3s, UDP fallback 3s, candidate probes 2s.
private let serverDiscoverBudget: TimeInterval = 8
/// The reminder fire's publish context.
private let serverReminderFireBudget: TimeInterval = 10
/// `http.Server.WriteTimeout`: no handler can answer later than this.
private let serverWriteTimeout: TimeInterval = 30

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
    #expect(RequestBudget.clockLong.requestTimeout > serverReminderFireBudget)
    #expect(RequestBudget.clockLong.resourceTimeout >= RequestBudget.clockLong.requestTimeout)
}

@Test(arguments: RequestBudget.allCases)
func defaultSessionMatchesItsBudget(budget: RequestBudget) {
    let client = APIClient(baseURL: URL(string: "http://example.local"), token: nil)
    let session = client.sessions(budget)
    #expect(session !== URLSession.shared)
    #expect(session.configuration.timeoutIntervalForRequest == budget.requestTimeout)
    #expect(session.configuration.timeoutIntervalForResource == budget.resourceTimeout)
}

// MARK: Which budget each call uses

/// Records method, path and the request's stamped timeout.
private final class RequestLog: @unchecked Sendable {
    private let lock = NSLock()
    private var entries: [String: TimeInterval] = [:]
    func add(_ req: URLRequest) {
        lock.withLock { entries["\(req.httpMethod ?? "") \(req.url?.path ?? "")"] = req.timeoutInterval }
    }
    subscript(_ key: String) -> TimeInterval? { lock.withLock { entries[key] } }
}

private func recordingDevice() -> (DeviceService, RequestLog) {
    let log = RequestLog()
    let client = stubbedClient { req in
        log.add(req)
        let body: String
        switch req.url?.path {
        case "/v1/device/apps": body = "[]"
        case "/v1/device/screen": body = #"{"width":32,"height":8,"pixels":[]}"#
        default: body = "{}"
        }
        return (okResponse(req.url!), Data(body.utf8))
    }
    return (DeviceService(client: client), log)
}

@Test func everyClockProxyCallGetsAClockBudget() async {
    let (device, log) = recordingDevice()
    // Decoding may fail on the stub's empty bodies; only the request matters.
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

    let clock = RequestBudget.clock.requestTimeout
    let long = RequestBudget.clockLong.requestTimeout
    let expected: [String: TimeInterval] = [
        "GET /v1/device/settings": clock,
        "PUT /v1/device/settings": long,
        "GET /v1/device/display": clock,
        "PUT /v1/device/display": clock,
        "PUT /v1/device/display/power": clock,
        "POST /v1/device/audio/test": clock,
        "POST /v1/device/audio/stop": clock,
        "GET /v1/device/audio/melodies": clock,
        "GET /v1/device/apps": clock,
        "PUT /v1/device/apps": clock,
        "GET /v1/device/capabilities": clock,
        "GET /v1/device/stats": clock,
        "GET /v1/device/sensors": clock,
        "PUT /v1/device/sensors": long,
        "GET /v1/device/screen": clock,
        "POST /v1/device/reboot": clock,
        "POST /v1/device/notify/dismiss": clock,
        "POST /v1/device/app/next": clock,
        "POST /v1/device/app/previous": clock,
        "GET /v1/device/discover": clock,
        "PUT /v1/device/buttons": long,
        "GET /v1/device/buttons": clock,
        // The clock URL lives on the server; setting it never calls the clock.
        "GET /v1/device/config": RequestBudget.server.requestTimeout,
        "PUT /v1/device/config": RequestBudget.server.requestTimeout,
    ]
    for (call, timeout) in expected {
        #expect(log[call] == timeout, "\(call)")
    }
}

@Test func plainServerCallsKeepTheServerBudget() async throws {
    let log = RequestLog()
    let client = stubbedClient { req in
        log.add(req)
        return (okResponse(req.url!), Data("{\"sessions\":[]}".utf8))
    }
    let _: Snapshot = try await client.get("/state")
    try await client.send("GET", "/healthz")
    #expect(log["GET /state"] == RequestBudget.server.requestTimeout)
    #expect(log["GET /healthz"] == RequestBudget.server.requestTimeout)
}

@Test func reminderFireGetsTheLongClockBudget() async throws {
    let log = RequestLog()
    let client = stubbedClient { req in
        log.add(req)
        return (okResponse(req.url!), Data())
    }
    try await client.postIdempotent("/v1/reminders/fire", body: ["x": 1], key: "k")
    #expect(log["POST /v1/reminders/fire"] == RequestBudget.clockLong.requestTimeout)
}

// MARK: Timeouts

@Test func timeoutIsItsOwnError() async {
    let client = stubbedClient { _ in throw URLError(.timedOut) }
    await #expect(throws: APIError.timedOut) { try await client.send("GET", "/v1/device/stats", budget: .clock) }
}

// A timeout is ambiguous (the server may have acted), so an idempotent POST
// must not claim it was never sent.
@Test func timedOutIdempotentPostIsNotNotSent() async {
    let client = stubbedClient { _ in throw URLError(.timedOut) }
    await #expect(throws: APIError.timedOut) {
        try await client.postIdempotent("/v1/reminders/fire", body: ["x": 1], key: "k")
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
    #expect(String(localized: subtitle).hasPrefix("Not responding since"))
    let plain = MenuRows.header(connection: .offline(since: since), hasEverLoaded: true, winning: nil,
                                offlineReason: .offline, locale: en, timeZone: utc)
    #expect(String(localized: plain.title).hasPrefix("Offline — server unreachable since"))
}

// MARK: Local Network precedence

// A request macOS refused is reported as the refusal under a clock budget too,
// never as a timeout or "unreachable".
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

// The classifier asks about a refusal before it looks at the code, so a
// refusal wins even over a timeout.
@Test func classifierPutsTheRefusalFirst() {
    let noAuth = NWError.dns(LocalNetworkDenial.dnsNoAuth)
    #expect(APIClient.classify(noAuth, host: "192.168.0.2", pathStatus: .satisfied) == .localNetworkDenied)
    #expect(APIClient.classify(URLError(.timedOut), host: "192.168.0.2", pathStatus: .satisfied) == .timedOut)
    let lost = APIClient.classify(URLError(.networkConnectionLost), host: "192.168.0.2", pathStatus: .satisfied)
    guard case .transport = lost else { Issue.record("got \(lost)"); return }
}
