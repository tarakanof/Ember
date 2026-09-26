import Testing
import Foundation
import Network
@testable import EmberKit

// A real awtrix-ng 1.1.2 `GET /api/v1/device` reply, captured read-only from
// the home clock. The fingerprint must accept it as-is.
private let ngDeviceFixture = #"""
{"version":"1.1.2","uid":"e868e705ffb8","boardType":"awtrixng","soc":"esp32",
"updateImage":"firmware-awtrix-ng.bin","ipAddress":"192.168.0.66","hostname":"Awtrix",
"wifiRssi":-78,"uptimeSeconds":341497,"freeHeapBytes":99628,"minFreeHeapBytes":76544,
"largestFreeBlockBytes":86004,"scriptingRunning":true,"scriptHeapPool":"internal",
"scriptHeapBudgetBytes":98304,"resetReason":"software","fps":42,"brightness":120,
"lightLevel":3.3,"ldrRaw":135,"batteryPercent":97,"batteryVoltage":4.17,
"batteryPinMillivolts":2331,"lowBattery":false,"temperature":33.3,"humidity":23.8,
"matrixPower":true,"currentApp":"ember",
"indicators":[{"on":false,"color":"#000000","blinkMs":0,"fadeMs":0},
{"on":false,"color":"#000000","blinkMs":0,"fadeMs":0},
{"on":false,"color":"#000000","blinkMs":0,"fadeMs":0}],
"messageCount":0,
"wifi":{"enabled":true,"state":"connected","host":"Akitaka","endpoint":"192.168.0.66","attempts":0,"retryInMs":0,"connects":1,"error":null,"lastError":null},
"mqtt":{"enabled":false,"state":"disabled","host":"","endpoint":"","attempts":0,"retryInMs":0,"connects":0,"error":null,"lastError":null}}
"""#

private func fingerprint(_ body: String, status: Int = 200) -> DiscoveredClock? {
    ClockDiscovery.candidate(name: "Awtrix", baseURL: "http://192.168.0.66:80", status: status, body: Data(body.utf8))
}

// MARK: Fingerprint — mirrors internal/discovery's probe (uid AND boardType)

@Test func clockFingerprintAcceptsARealNGDevice() {
    let c = fingerprint(ngDeviceFixture)
    #expect(c == DiscoveredClock(host: "Awtrix", baseURL: "http://192.168.0.66:80",
                                 uid: "e868e705ffb8", version: "1.1.2"))
}

// The uid alone is not enough: /api/v1/device is a generic-looking path.
@Test func clockFingerprintRejectsAnotherBoardType() {
    #expect(fingerprint(#"{"uid":"abc","boardType":"esphome","version":"1"}"#) == nil)
    #expect(fingerprint(#"{"uid":"abc","version":"1"}"#) == nil)
}

@Test func clockFingerprintRejectsAMissingOrEmptyUID() {
    #expect(fingerprint(#"{"uid":"","boardType":"awtrixng"}"#) == nil)
    #expect(fingerprint(#"{"boardType":"awtrixng"}"#) == nil)
}

// The server accepts any 2xx (awtrix.checkStatus) and nothing else.
@Test func clockFingerprintRejectsANon2xxStatus() {
    #expect(fingerprint(ngDeviceFixture, status: 404) == nil)
    #expect(fingerprint(ngDeviceFixture, status: 500) == nil)
    #expect(fingerprint(ngDeviceFixture, status: 203) != nil)
}

@Test func clockFingerprintRejectsANonJSONBody() {
    #expect(fingerprint("<html>printer</html>") == nil)
}

@Test func clockFingerprintToleratesAMissingVersion() {
    #expect(fingerprint(#"{"uid":"u","boardType":"awtrixng"}"#)?.version == "")
}

// MARK: Base URL — byte-identical to the server's baseURLFor for IPv4

@Test func clockBaseURLKeepsAnExplicitPort() {
    #expect(ClockDiscovery.baseURL(host: "192.168.0.66", port: 80) == "http://192.168.0.66:80")
    #expect(ClockDiscovery.baseURL(host: "192.168.0.66", port: 8080) == "http://192.168.0.66:8080")
}

// dnssd reports port 0 for a record with no port; the server falls back to 80.
@Test func clockBaseURLDefaultsAMissingPortTo80() {
    #expect(ClockDiscovery.baseURL(host: "192.168.0.66", port: 0) == "http://192.168.0.66:80")
}

// The resolve pins IPv4; anything else is not a URL the server can use.
@Test func clockBaseURLRejectsNonIPv4Hosts() {
    #expect(ClockDiscovery.baseURL(host: "", port: 80) == nil)
    #expect(ClockDiscovery.baseURL(host: "fe80::1%en0", port: 80) == nil)
    #expect(ClockDiscovery.baseURL(host: "Awtrix.local.", port: 80) == nil)
}

// MARK: Probe request (stubbed transport — no LAN traffic)

@Test func clockProbeGetsTheDeviceRouteWithAShortTimeout() async {
    let host = "stub-\(UUID().uuidString.lowercased()).local"
    StubURLProtocol.register(host: host) { req in
        #expect(req.httpMethod == "GET")
        #expect(req.url?.path == "/api/v1/device")
        #expect(req.timeoutInterval == 1.5)
        return (okResponse(req.url!), Data(ngDeviceFixture.utf8))
    }
    let c = await ClockDiscovery.probe(name: "Awtrix", baseURL: "http://\(host):80", session: stubSession())
    #expect(c?.uid == "e868e705ffb8")
    #expect(c?.baseURL == "http://\(host):80")
}

@Test func clockProbeTreatsATransportFailureAsNotAClock() async {
    let host = "stub-\(UUID().uuidString.lowercased()).local"
    StubURLProtocol.register(host: host) { _ in throw URLError(.cannotConnectToHost) }
    #expect(await ClockDiscovery.probe(name: "x", baseURL: "http://\(host):80", session: stubSession()) == nil)
}

// MARK: Merge — server and app results, de-duplicated by uid, labelled by source

private func clock(_ host: String, _ base: String, uid: String) -> DiscoveredClock {
    DiscoveredClock(host: host, baseURL: base, uid: uid, version: "1.1.2")
}

@Test func clockMergeDeDupesByUIDAndPrefersTheServersAddress() {
    let server = [clock("Awtrix", "http://192.168.0.66:80", uid: "u1")]
    let mac = [clock("Awtrix", "http://10.0.0.66:80", uid: "u1")]
    let merged = ClockChoice.merge(server: server, mac: mac)
    #expect(merged.count == 1)
    #expect(merged[0].source == .both)
    // The server can reach the address it found; saving it is the safe pick.
    #expect(merged[0].clock.baseURL == "http://192.168.0.66:80")
}

@Test func clockMergeLabelsEachSource() {
    let merged = ClockChoice.merge(server: [clock("Kitchen", "http://10.0.0.2:80", uid: "a")],
                                   mac: [clock("Desk", "http://10.0.0.3:80", uid: "b")])
    #expect(merged.map(\.clock.host) == ["Desk", "Kitchen"])
    #expect(merged.map(\.source) == [.mac, .server])
}

@Test func clockMergeDeDupesWithinOneSource() {
    let merged = ClockChoice.merge(server: [], mac: [clock("A", "http://10.0.0.2:80", uid: "a"),
                                                     clock("A", "http://10.0.0.9:80", uid: "a")])
    #expect(merged.map(\.clock.baseURL) == ["http://10.0.0.2:80"])
}

// Probes land in whatever order the LAN answers; the list must not reshuffle.
@Test func clockMergeOrdersByHostThenAddress() {
    let list = ClockDiscovery.merged(ClockDiscovery.merged([], adding: clock("b", "http://10.0.0.1:80", uid: "2")),
                                     adding: clock("a", "http://10.0.0.9:80", uid: "1"))
    #expect(list.map(\.uid) == ["1", "2"])
    let tie = ClockDiscovery.merged(ClockDiscovery.merged([], adding: clock("a", "http://10.0.0.9:80", uid: "2")),
                                    adding: clock("a", "http://10.0.0.1:80", uid: "1"))
    #expect(tie.map(\.uid) == ["1", "2"])
}

// Discovery always emits a port; a hand-entered address often doesn't.
@Test func clockURLMatchesAPortlessConfiguredAddress() {
    #expect(ClockURL.same("http://192.168.0.66:80", "http://192.168.0.66"))
    #expect(ClockURL.same("http://192.168.0.66:80", "http://192.168.0.66/"))
    #expect(!ClockURL.same("http://192.168.0.66:80", "http://192.168.0.67"))
    #expect(!ClockURL.same("http://192.168.0.66:8080", "http://192.168.0.66"))
    #expect(!ClockURL.same("http://192.168.0.66:80", nil))
}

// MARK: When to offer "Find clock from this Mac"

private let reachableFalse = #""device":{"reachable":false,"checked_at":"2026-09-26T10:00:00Z"}"#
private let reachableTrue = #""device":{"reachable":true,"checked_at":"2026-09-26T10:00:00Z"}"#

private func lost(_ health: Loadable<ClockHealth>, settingsLoaded: Bool = true,
                  settingsError: FeedError? = nil) -> Bool {
    ClockDiscovery.serverLostClock(health: health, settingsLoaded: settingsLoaded, settingsError: settingsError)
}

private func loaded(_ h: ClockHealth) -> Loadable<ClockHealth> { .loaded(h, at: Date()) }

@Test func serverLostClockWhenTheServerHasNoClock() throws {
    #expect(lost(loaded(try healthJSON(#""device":null"#))))
}

// The clock's Wi-Fi drops requests and the server caches one probe for 30 s:
// a failed probe alone must not raise the prompt, only with a failed push too.
@Test func serverLostClockNeedsAFailedProbeAndAFailedPush() throws {
    #expect(!lost(loaded(try healthJSON(reachableFalse, lastOk: true))))
    #expect(lost(loaded(try healthJSON(reachableFalse, lastOk: false))))
    #expect(!lost(loaded(try healthJSON(reachableTrue, lastOk: false))))
}

// A stale value kept after the health feed failed says nothing about now
// (the server itself may be what's down).
@Test func serverLostClockIgnoresStaleHealth() throws {
    let bad = try healthJSON(reachableFalse, lastOk: false)
    #expect(!lost(.failed(.offline, last: bad, lastAt: Date())))
    #expect(!lost(.loading))
}

// A proxied settings read that failed on the server's side (502) means the
// clock; an unreachable server or a bad token is not something discovery fixes.
@Test func serverLostClockWhenTheSettingsProxyFails() {
    #expect(lost(.loading, settingsLoaded: false, settingsError: .server("HTTP 502")))
    #expect(!lost(.loading, settingsLoaded: false, settingsError: .offline))
    #expect(!lost(.loading, settingsLoaded: false, settingsError: .unauthorized))
    #expect(!lost(.loading, settingsLoaded: false, settingsError: nil))
}

private func healthJSON(_ device: String, lastOk: Bool = true) throws -> ClockHealth {
    let json = """
    {"generated_at":"2026-09-26T10:00:00Z",
     "publish":{"counting_since":"2026-09-26T09:00:00Z","ok_24h":1,"fail_24h":0,"success_ratio_24h":1,
                "ok_total":1,"fail_total":0,"retries_total":0,"last_at":null,"last_ok":\(lastOk)},
     \(device)}
    """
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try d.decode(ClockHealth.self, from: Data(json.utf8))
}

// MARK: Resolve states — keep waiting, give up only on failure or denial

// A lost mDNS answer parks the connection in .waiting; NWConnection retries
// on its own and the scan window bounds it, so it must not be dropped.
@Test func resolveKeepsWaitingOnATransientError() {
    #expect(BonjourClockBrowser.step(for: .waiting(.posix(.ENETUNREACH))) == .keepWaiting)
    #expect(BonjourClockBrowser.step(for: .waiting(.dns(DNSServiceErrorType(kDNSServiceErr_Timeout)))) == .keepWaiting)
    #expect(BonjourClockBrowser.step(for: .preparing) == .ignore)
    #expect(BonjourClockBrowser.step(for: .ready) == .resolved)
}

@Test func resolveReportsLocalNetworkDenial() {
    let denied = NWError.dns(DNSServiceErrorType(kDNSServiceErr_PolicyDenied))
    #expect(BonjourClockBrowser.step(for: .waiting(denied)) == .denied)
    #expect(BonjourClockBrowser.step(for: .failed(denied)) == .denied)
    #expect(BonjourClockBrowser.step(for: .failed(.posix(.ECONNREFUSED))) == .failed)
}

// MARK: Lifecycle (#61) — a fake browser, a hand-driven clock, no mDNS

@MainActor
private final class FakeBrowser: ClockBrowsing {
    var started = 0
    var cancelled = 0
    var onState: (@MainActor (ClockBrowseState) -> Void)?
    var onResolved: (@MainActor (ClockService) -> Void)?

    func start(onState: @escaping @MainActor (ClockBrowseState) -> Void,
               onResolved: @escaping @MainActor (ClockService) -> Void) {
        started += 1
        self.onState = onState
        self.onResolved = onResolved
    }
    func cancel() { cancelled += 1 }
    func resolve(_ host: String, port: Int = 80, name: String = "Awtrix") {
        onResolved?(ClockService(name: name, host: host, port: port))
    }
}

/// Probes that answer as a clock (uid = host) once `release` is called for
/// that base URL, so a test can hold one in flight.
@MainActor
private final class HeldProbes {
    var calls: [String] = []
    private var waiting: [String: CheckedContinuation<Void, Never>] = [:]
    private var released: Set<String> = []

    func probe(_ name: String, _ base: String) async -> DiscoveredClock? {
        calls.append(base)
        if !released.contains(base) {
            // Honours cancellation the way URLSession does.
            await withTaskCancellationHandler {
                await withCheckedContinuation { c in
                    if Task.isCancelled { c.resume() } else { waiting[base] = c }
                }
            } onCancel: {
                Task { @MainActor in self.wake(base) }
            }
        }
        if Task.isCancelled { return nil }
        return DiscoveredClock(host: name, baseURL: base, uid: base, version: "1.1.2")
    }
    func release(_ base: String) {
        released.insert(base)
        wake(base)
    }
    private func wake(_ base: String) { waiting.removeValue(forKey: base)?.resume() }
    func releaseAll() { for b in Array(waiting.keys) { release(b) } }
}

@MainActor
private func makeDiscovery() -> (ClockDiscovery, FakeBrowser, HeldProbes, ManualClock) {
    let browser = FakeBrowser()
    let probes = HeldProbes()
    let clock = ManualClock()
    let d = ClockDiscovery(makeBrowser: { browser },
                           probe: { name, base in await probes.probe(name, base) },
                           sleep: clock.sleepFn)
    return (d, browser, probes, clock)
}

@MainActor @Test func scanStopsBrowsingWhenTheWindowEndsAndKeepsResults() async {
    let (d, browser, probes, clock) = makeDiscovery()
    probes.release("http://192.168.0.66:80")
    let scan = Task { await d.scan() }
    await clock.settle()
    #expect(browser.started == 1)
    #expect(d.isScanning)
    browser.resolve("192.168.0.66")
    await clock.settle()
    #expect(d.clocks.map(\.baseURL) == ["http://192.168.0.66:80"])
    await clock.advance(by: ClockDiscovery.browseWindow)
    await scan.value
    #expect(browser.cancelled == 1)
    #expect(!d.isScanning)
    // A scan that ran its course keeps its list for the user to pick from.
    #expect(d.clocks.count == 1)
}

// NWBrowser replays results; one address is probed once per scan.
@MainActor @Test func scanProbesEachAddressOnce() async {
    let (d, browser, probes, clock) = makeDiscovery()
    probes.release("http://192.168.0.66:80")
    let scan = Task { await d.scan() }
    await clock.settle()
    browser.resolve("192.168.0.66")
    browser.resolve("192.168.0.66")
    browser.resolve("192.168.0.66", port: 0)
    await clock.settle()
    #expect(probes.calls == ["http://192.168.0.66:80"])
    d.stop()
    await clock.advance(by: ClockDiscovery.browseWindow)
    await scan.value
}

@MainActor @Test func stopCancelsTheBrowseAndDropsLateProbes() async {
    let (d, browser, probes, clock) = makeDiscovery()
    let scan = Task { await d.scan() }
    await clock.settle()
    browser.resolve("192.168.0.66")
    await clock.settle()
    #expect(probes.calls.count == 1)
    d.stop()
    #expect(browser.cancelled == 1)
    #expect(!d.isScanning)
    probes.releaseAll()          // the in-flight probe answers after the stop
    await clock.settle()
    #expect(d.clocks.isEmpty)
    // Resolutions after the stop go nowhere.
    browser.resolve("192.168.0.67")
    await clock.settle()
    #expect(probes.calls.count == 1)
    await clock.advance(by: ClockDiscovery.browseWindow)
    await scan.value
    #expect(d.clocks.isEmpty)
}

// The sheet runs scan() in `.task`: dismissing it cancels the task, and that
// alone must tear the browse down (no reliance on .onDisappear).
@MainActor @Test func cancellingTheScanTaskTearsDown() async {
    let (d, browser, _, clock) = makeDiscovery()
    let scan = Task { await d.scan() }
    await clock.settle()
    browser.resolve("192.168.0.66")
    await clock.settle()
    scan.cancel()
    await scan.value
    #expect(browser.cancelled == 1)
    #expect(!d.isScanning)
    #expect(d.clocks.isEmpty)
}

// A probe still out when the browse window closes gets a bounded grace, then
// is cancelled — the scan always ends.
@MainActor @Test func scanCancelsProbesStillOutAfterTheGrace() async {
    let (d, browser, probes, clock) = makeDiscovery()
    let scan = Task { await d.scan() }
    await clock.settle()
    browser.resolve("192.168.0.66")
    await clock.advance(by: ClockDiscovery.browseWindow)
    #expect(d.isScanning)                        // waiting on the probe
    await clock.advance(by: ClockDiscovery.probeGrace)
    await scan.value
    #expect(!d.isScanning)
    #expect(d.clocks.isEmpty)
    probes.releaseAll()
}

// A probe that lands inside the grace is kept.
@MainActor @Test func scanKeepsAProbeThatLandsInTheGrace() async {
    let (d, browser, probes, clock) = makeDiscovery()
    let scan = Task { await d.scan() }
    await clock.settle()
    browser.resolve("192.168.0.66")
    await clock.advance(by: ClockDiscovery.browseWindow)
    probes.release("http://192.168.0.66:80")
    await scan.value
    #expect(d.clocks.map(\.uid) == ["http://192.168.0.66:80"])
}

// Search Again restarts cleanly: the old browse is cancelled, a new one runs.
@MainActor @Test func aNewScanSupersedesTheRunningOne() async {
    let (d, browser, _, clock) = makeDiscovery()
    let first = Task { await d.scan() }
    await clock.settle()
    let second = Task { await d.scan() }
    await clock.settle()
    #expect(browser.started == 2)
    #expect(browser.cancelled == 1)
    #expect(d.isScanning)
    await clock.advance(by: ClockDiscovery.browseWindow)
    await first.value
    await second.value
    #expect(!d.isScanning)
}

@MainActor @Test func aWaitingBrowseReportsLocalNetworkAccess() async {
    let (d, browser, _, clock) = makeDiscovery()
    let scan = Task { await d.scan() }
    await clock.settle()
    browser.onState?(.waiting)
    #expect(d.access == .needsAccess)
    browser.onState?(.ready)
    #expect(d.access == .ok)
    browser.onState?(.failed)
    #expect(d.access == .unavailable)
    await clock.advance(by: ClockDiscovery.browseWindow)
    await scan.value
    // The hint outlives the window, so the sheet can still show it.
    #expect(d.access == .unavailable)
}

// MARK: Choosing a clock — PUT /v1/device/config through the existing path

private struct DeviceConfigBody: Decodable, Equatable { let base_url: String }

@MainActor @Test func usingAMacFoundClockPutsItsBaseURL() async {
    let box = LockedBox()
    let client = stubbedClient(token: "t") { req in
        box.add("\(req.httpMethod ?? "") \(req.url!.path)")
        if req.httpMethod == "PUT" {
            #expect(req.url?.path == "/v1/device/config")
            #expect(req.value(forHTTPHeaderField: "Authorization") == "Bearer t")
            let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
            let sent = try JSONDecoder().decode(DeviceConfigBody.self, from: body)
            #expect(sent == DeviceConfigBody(base_url: "http://192.168.0.66:80"))
            return (okResponse(req.url!, status: 204), Data())
        }
        return (okResponse(req.url!, status: 502), Data())
    }
    let m = DeviceSettingsModel(service: DeviceService(client: client), debounce: .milliseconds(600),
                                sleep: ManualClock().sleepFn, now: { Date() })
    await m.use(clock("Awtrix", "http://192.168.0.66:80", uid: "e868e705ffb8"))
    #expect(m.actionErrors[.useClock] == nil)
    #expect(box.paths.first == "PUT /v1/device/config")
}

// Rows from a finished scan must not show when the next one starts.
@MainActor @Test func aNewScanStartsWithAnEmptyList() async {
    let (d, browser, probes, clock) = makeDiscovery()
    probes.release("http://192.168.0.66:80")
    let first = Task { await d.scan() }
    await clock.settle()
    browser.resolve("192.168.0.66")
    await clock.advance(by: ClockDiscovery.browseWindow)
    await first.value
    #expect(d.clocks.count == 1)
    let second = Task { await d.scan() }
    await clock.settle()
    #expect(d.clocks.isEmpty)
    d.stop()
    await clock.advance(by: ClockDiscovery.browseWindow)
    await second.value
}
