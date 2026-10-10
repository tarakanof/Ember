import Testing
import Foundation
@testable import EmberKit

@Test func knobPatchMergesNestedFields() {
    let old = KnobSettings.defaults
    var new = old
    new.brightness.level = 100
    new.bot.demoHoldS = 30
    #expect(new.patch(from: old) == [
        "brightness": .object(["level": .int(100)]),
        "bot": .object(["demo_hold_s": .int(30)]),
    ])
}

@Test func knobPatchSendsPagesWhole() {
    let old = KnobSettings.defaults
    var new = old
    new.pages.swapAt(0, 3)
    let patch = new.patch(from: old)
    #expect(patch.keys.sorted() == ["pages"])
    guard case .array(let pages)? = patch["pages"] else { Issue.record("pages"); return }
    #expect(pages.count == 4)
    #expect(KnobSettings.defaults.patch(from: .defaults).isEmpty)
}

@Test func knobNormalizeKeepsServerRules() {
    var s = KnobSettings.defaults
    s.brightness.floor = 200
    s.brightness.level = 100
    #expect(s.normalized().brightness.level == 200)
    s = .defaults
    s.pages[0].on = false
    #expect(s.normalized().home == "pomodoro")
    s.pages = s.pages.map { KnobSettings.Page(id: $0.id, on: false) }
    let n = s.normalized()
    #expect(n.pages.filter(\.on).count == 1)
    #expect(n.home == n.pages.first(where: \.on)?.id)
    #expect(KnobSettings.defaults.isLastPageOn("bot") == false)
}

@Test func knobMovePages() {
    var s = KnobSettings.defaults
    let ids = s.pages.map(\.id)
    let home = s.home
    s.movePage(ids[0], by: -1)
    s.movePage(ids[3], by: 1)
    #expect(s.pages.map(\.id) == ids)
    s.movePage(ids[0], by: 1)
    #expect(s.pages.map(\.id) == [ids[1], ids[0], ids[2], ids[3]])
    s = .defaults
    s.movePage(ids[0], to: ids[2])
    #expect(s.pages.map(\.id) == [ids[1], ids[2], ids[0], ids[3]])
    s.movePage(ids[0], to: ids[1])
    #expect(s.pages.map(\.id) == [ids[0], ids[1], ids[2], ids[3]])
    s.movePage("nope", to: ids[0])
    s.movePage(ids[0], to: ids[0])
    #expect(s.pages.map(\.id) == ids)
    #expect(s.home == home)
    s.movePage(ids[1], by: -1)
    #expect(s.patch(from: .defaults).keys.sorted() == ["pages"])
}

@Test func knobSettingsDecodeServerJSON() throws {
    let json = #"{"brightness":{"follow_ember":false,"level":200,"floor":5,"startup":120},"pages":[{"id":"weather","on":true},{"id":"bot","on":false}],"home":"weather","poll_ms":3000,"bot":{"sleepy_after_s":0,"demo_hold_s":15}}"#
    let s = try JSONDecoder().decode(KnobSettings.self, from: Data(json.utf8))
    #expect(s.brightness.followEmber == false)
    #expect(s.pages.map(\.id) == ["weather", "bot"])
    #expect(s.pollMS == 3000)
    #expect(s.bot.sleepyAfterS == 0)
    #expect(s.bot.sourceLabel == nil && s.bot.workingRing == nil)
}

@Test func knobDisplayLinkDecodes() throws {
    let s = try JSONDecoder().decode(KnobSettings.self, from: Data(#"{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[],"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20},"display":{"fast_link":false}}"#.utf8))
    #expect(s.display?.fastLink == false)
    let old = try JSONDecoder().decode(KnobSettings.self, from: Data(#"{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[],"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20}}"#.utf8))
    #expect(old.display == nil)
    #expect(!String(decoding: try JSONEncoder().encode(old), as: UTF8.self).contains("display"))
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let c = try d.decode(KnobCheckin.self, from: Data(#"{"seen_at":"2026-10-05T10:00:00Z","fw":"0.9.2","ip":"","rssi":-60,"heap_internal_free":1,"heap_internal_largest":1,"uptime_s":1,"applied_version":1,"link_mhz":40,"link_fallback":true}"#.utf8))
    #expect(c.linkMHz == 40 && c.linkFallback == true)
}

@Test func knobQuietDecodesAndPatchesOnlyWhatChanged() throws {
    let base = #""brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[{"id":"bot","on":true}],"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20}"#
    let s = try JSONDecoder().decode(KnobSettings.self, from: Data(("{" + base + #","quiet":{"calm":true,"dim_level":20}}"#).utf8))
    #expect(s.quiet == KnobSettings.Quiet(calm: true, dimLevel: 20))
    var edited = s
    edited.quiet?.dimLevel = 0
    edited = edited.normalized()
    #expect(edited.quiet?.dimLevel == KnobSettings.quietDimRange.lowerBound)
    edited.quiet?.calm = false
    #expect(edited.patch(from: s) == ["quiet": .object(["calm": .bool(false), "dim_level": .int(1)])])
    let old = try JSONDecoder().decode(KnobSettings.self, from: Data(("{" + base + "}").utf8))
    #expect(old.quiet == nil)
    #expect(!String(decoding: try JSONEncoder().encode(old), as: UTF8.self).contains("quiet"))
}

@Test func knobCheckinDiagDecodes() throws {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let base = #""seen_at":"2026-10-06T10:00:00Z","fw":"0.9.13","ip":"","rssi":-74,"heap_internal_free":1,"heap_internal_largest":1,"uptime_s":1,"applied_version":1"#
    let full = try d.decode(KnobCheckin.self, from: Data(("{" + base + #","diag":{"boots":42,"crash":{"pc":"0x4201a2b3","reason":"panic","task":"ember"},"heap_internal_min":71234,"heap_largest_min":30720,"reset_reason":"poweron","stack_free":{"ember":1880,"eye":900,"lvgl":2304},"reboots":3,"prev_reset_reason":"task_wdt"}}"#).utf8))
    #expect(full.diag == KnobDiag(boots: 42, crash: KnobCrash(pc: "0x4201a2b3", reason: "panic", task: "ember"),
                                  heapInternalMin: 71234, heapLargestMin: 30720, resetReason: "poweron",
                                  stackFree: ["ember": 1880, "eye": 900, "lvgl": 2304],
                                  reboots: 3, prevResetReason: "task_wdt"))
    let sparse = try d.decode(KnobCheckin.self, from: Data(("{" + base + #","diag":{}}"#).utf8))
    #expect(sparse.diag == KnobDiag())
    let gap = try d.decode(KnobCheckin.self, from: Data(("{" + base + #","diag":{"boots":44,"reset_reason":"sw","reboots":4,"reboots_since_seen":3}}"#).utf8))
    #expect(gap.diag?.rebootsSinceSeen == 3 && gap.diag?.prevResetReason == nil)
    let old = try d.decode(KnobCheckin.self, from: Data(("{" + base + "}").utf8))
    #expect(old.diag == nil)
}

@Test func knobDiagHidesUnknownDetails() {
    #expect(KnobDiag().unseenRestarts == nil)
    #expect(KnobDiag(rebootsSinceSeen: 1).unseenRestarts == nil)
    #expect(KnobDiag(rebootsSinceSeen: 2).unseenRestarts == 2)
    #expect(KnobDiag(heapInternalMin: 70000).largestBlockMin == nil)
    #expect(KnobDiag(heapInternalMin: 70000, heapLargestMin: 0).largestBlockMin == nil)
    #expect(KnobDiag(heapInternalMin: 70000, heapLargestMin: 30720).largestBlockMin == 30720)
}

@Test func knobCheckinWifiDecodes() throws {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let base = #""seen_at":"2026-10-06T10:00:00Z","fw":"0.9.8","ip":"","rssi":-74,"heap_internal_free":1,"heap_internal_largest":1,"uptime_s":1,"applied_version":1"#
    let full = try d.decode(KnobCheckin.self, from: Data(("{" + base + #","wifi":{"bssid":"78:45:58:4b:c2:cd","channel":6,"disconnects":3,"last_reason":203,"rssi_min":-83}}"#).utf8))
    #expect(full.wifi == KnobWifi(bssid: "78:45:58:4b:c2:cd", channel: 6, disconnects: 3, lastReason: 203, rssiMin: -83))
    let sparse = try d.decode(KnobCheckin.self, from: Data(("{" + base + #","wifi":{"disconnects":0}}"#).utf8))
    #expect(sparse.wifi == KnobWifi(disconnects: 0))
    #expect(full.wifi?.hasLinkDetails == true && sparse.wifi?.hasLinkDetails == false)
    #expect(KnobWifi(channel: 6, disconnects: 0).hasLinkDetails && KnobWifi(disconnects: 0, rssiMin: -83).hasLinkDetails)
    let old = try d.decode(KnobCheckin.self, from: Data(("{" + base + "}").utf8))
    #expect(old.wifi == nil)
}

@Test func knobBotFlagsDecodeAndStayOffTheWireWhenAbsent() throws {
    let json = #"{"sleepy_after_s":300,"demo_hold_s":20,"source_label":false,"working_ring":true}"#
    let b = try JSONDecoder().decode(KnobSettings.Bot.self, from: Data(json.utf8))
    #expect(b.sourceLabel == false && b.workingRing == true)
    let old = String(decoding: try JSONEncoder().encode(KnobSettings.Bot(sleepyAfterS: 300, demoHoldS: 20)), as: UTF8.self)
    #expect(!old.contains("source_label") && !old.contains("working_ring"))
}

private let deviceJSON = #"""
{"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"3cdc7561fc8c","name":"Desk knob","created_at":"2026-10-04T10:00:00Z","config_version":3,"rotation_pending":false,"rotated_at":null,"last_checkin":{"seen_at":"2026-10-04T10:05:00Z","fw":"0.5.0","ip":"192.168.0.39","rssi":-58,"heap_internal_free":47104,"heap_internal_largest":31744,"uptime_s":812,"applied_version":2}}
"""#

@Test func knobDeviceDecodes() throws {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let k = try d.decode(KnobDevice.self, from: Data(deviceJSON.utf8))
    #expect(k.shortID == "61FC8C")
    #expect(k.lastCheckin?.rssi == -58)
    #expect(!k.configApplied)
    let seen = k.lastCheckin!.seenAt
    #expect(k.isOnline(now: seen.addingTimeInterval(60)))
    #expect(!k.isOnline(now: seen.addingTimeInterval(600)))
}

@Test func currentKnobIsTheNewest() {
    let a = KnobDevice(id: "a", hwID: "000000000001", name: "A", createdAt: Date(timeIntervalSince1970: 10))
    let b = KnobDevice(id: "b", hwID: "000000000002", name: "B", createdAt: Date(timeIntervalSince1970: 20))
    let other = KnobDevice(id: "c", kind: "other", hwID: "000000000003", name: "C", createdAt: Date(timeIntervalSince1970: 30))
    #expect(KnobDevice.current(in: [b, a, other])?.id == "b")
    #expect(KnobDevice.current(in: []) == nil)
}

private final class FakeRegistry: @unchecked Sendable {
    private let lock = NSLock()
    var devices: [String] = [deviceJSON]
    private(set) var requests: [(String, String, String)] = []
    var config = #"{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true}],"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20}}"#

    var log: [(String, String, String)] { lock.withLock { requests } }

    func handle(_ req: URLRequest) -> (HTTPURLResponse, Data) {
        let body = String(decoding: req.httpBody ?? req.httpBodyStreamData() ?? Data(), as: UTF8.self)
        let method = req.httpMethod ?? "GET"
        let path = req.url!.path
        lock.withLock { requests.append((method, path, body)) }
        #expect(req.value(forHTTPHeaderField: "Authorization") == "Bearer t")
        switch (method, path) {
        case ("GET", "/v1/devices"):
            let list = lock.withLock { devices.joined(separator: ",") }
            return (okResponse(req.url!), Data(#"{"devices":[\#(list)]}"#.utf8))
        case ("GET", "/v1/devices/knob-61fc8c/config"), ("PUT", "/v1/devices/knob-61fc8c/config"):
            return (okResponse(req.url!), Data(config.utf8))
        case ("POST", "/v1/devices"):
            let minted = deviceJSON.dropLast() + #","token":"ekd_x"}"#
            return (okResponse(req.url!, status: 201), Data(minted.utf8))
        case ("PATCH", "/v1/devices/knob-61fc8c"):
            return (okResponse(req.url!), Data(deviceJSON.replacingOccurrences(of: "Desk knob", with: "Shelf knob").utf8))
        case ("POST", "/v1/devices/knob-61fc8c/rotate"):
            return (okResponse(req.url!, status: 202), Data(deviceJSON.utf8))
        case ("DELETE", "/v1/devices/knob-61fc8c"):
            lock.withLock { devices = [] }
            return (okResponse(req.url!, status: 204), Data())
        default:
            return (okResponse(req.url!, status: 404), Data(#"{"error":"not found"}"#.utf8))
        }
    }
}

@MainActor
private func model(_ fake: FakeRegistry) -> KnobModel {
    let svc = KnobService(client: stubbedClient(token: "t") { fake.handle($0) })
    return KnobModel(service: svc, ports: KnobSerialPorts(scanner: { [] }),
                     opener: FakeOpener(FakeKnob()), debounce: .zero, sleep: { _ in })
}

@Test func knobServiceMintsWithKindAndHwID() async throws {
    let fake = FakeRegistry()
    let svc = KnobService(client: stubbedClient(token: "t") { fake.handle($0) })
    let minted = try await svc.mint(hwID: "3cdc7561fc8c", name: "Desk knob")
    #expect(minted.token == "ekd_x")
    #expect(minted.device.id == "knob-61fc8c")
    let body = try #require(fake.log.last?.2)
    let obj = try #require(try JSONSerialization.jsonObject(with: Data(body.utf8)) as? [String: String])
    #expect(obj == ["kind": "cinder-knob", "hw_id": "3cdc7561fc8c", "name": "Desk knob"])
}

@MainActor
@Test func knobModelLoadsAndSavesAMergePatch() async throws {
    let fake = FakeRegistry()
    let m = model(fake)
    await m.load()
    #expect(m.isLoaded)
    #expect(m.knob?.name == "Desk knob")
    #expect(m.settings.isLoaded)
    m.edit { $0.brightness.followEmber = false }
    await m.settings.saveNow()
    let put = try #require(fake.log.last { $0.0 == "PUT" })
    #expect(put.1 == "/v1/devices/knob-61fc8c/config")
    #expect(put.2 == #"{"brightness":{"follow_ember":false}}"#)
}

@Test func knobRotationDecodesAndPatchesAlone() throws {
    let base = #""brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[{"id":"bot","on":true}],"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20}"#
    let s = try JSONDecoder().decode(KnobSettings.self, from: Data(("{" + base + #","display":{"fast_link":true,"rotation":0}}"#).utf8))
    #expect(s.display == KnobSettings.Display(fastLink: true, rotation: 0))
    var edited = s
    edited.display?.rotation = 180
    #expect(edited.patch(from: s) == ["display": .object(["rotation": .int(180)])])
    edited.display?.fastLink = false
    #expect(edited.display?.rotation == 180)
    let old = try JSONDecoder().decode(KnobSettings.self, from: Data(("{" + base + #","display":{"fast_link":true}}"#).utf8))
    #expect(old.display?.rotation == nil)
    #expect(!String(decoding: try JSONEncoder().encode(old), as: UTF8.self).contains("rotation"))
}

@MainActor
@Test func knobModelSavesRotation() async throws {
    let fake = FakeRegistry()
    fake.config = String(fake.config.dropLast()) + #","display":{"fast_link":true,"rotation":0}}"#
    let m = model(fake)
    await m.load()
    #expect(m.settings.draft.display?.rotation == 0)
    m.edit { $0.display?.rotation = 180 }
    await m.settings.saveNow()
    let put = try #require(fake.log.last { $0.0 == "PUT" })
    #expect(put.1 == "/v1/devices/knob-61fc8c/config")
    #expect(put.2 == #"{"display":{"rotation":180}}"#)
}

@MainActor
@Test func knobModelEmptyWhenNothingRegistered() async {
    let fake = FakeRegistry()
    fake.devices = []
    let m = model(fake)
    await m.load()
    #expect(m.isLoaded)
    #expect(m.knob == nil)
    #expect(!fake.log.contains { $0.1.hasSuffix("/config") })
}

@MainActor
@Test func knobModelRenameRotateForget() async {
    let fake = FakeRegistry()
    let m = model(fake)
    await m.load()
    await m.rename("  Shelf knob ")
    #expect(m.knob?.name == "Shelf knob")
    #expect(fake.log.contains { $0.0 == "PATCH" && $0.2 == #"{"name":"Shelf knob"}"# })
    await m.rotate()
    #expect(m.actionErrors[.rotate] == nil)
    #expect(fake.log.contains { $0.0 == "POST" && $0.1.hasSuffix("/rotate") })
    #expect(await m.forget())
    #expect(m.knob == nil)
}

@MainActor
@Test func knobModelReportsUnauthorized() async {
    let svc = KnobService(client: stubbedClient(token: "bad") { req in
        (okResponse(req.url!, status: 401), Data(#"{"error":"unauthorized"}"#.utf8))
    })
    let m = KnobModel(service: svc, ports: KnobSerialPorts(scanner: { [] }), opener: FakeOpener(FakeKnob()),
                      debounce: .zero, sleep: { _ in })
    await m.load()
    #expect(m.loadError == .unauthorized)
    #expect(!m.isLoaded)
}

@MainActor
@Test func knobModelProbesPluggedInBoards() async {
    let port = KnobSerialPort(path: "/dev/cu.fake", vendorID: 0x303A, productID: 0x1001, serialNumber: "3C:DC:75:61:FC:8C")
    let silent = FakeKnob()
    let fake = FakeRegistry()
    let svc = KnobService(client: stubbedClient(token: "t") { fake.handle($0) })
    let ports = KnobSerialPorts(scanner: { [port] })
    ports.rescan()
    let m = KnobModel(service: svc, ports: ports, opener: FakeOpener(silent), debounce: .zero, sleep: { _ in })
    var t = KnobProvisioner.Timeouts()
    t.info = .milliseconds(50)
    m.probeTimeouts = t
    await m.probePorts()
    #expect(m.portStatus[port.path] == .notCinder)
    #expect(m.connectedPort == port)
}

@Test func emberURLSwapsLoopbackAndMDNSForLANAddresses() {
    let local = KnobEmberURL.suggest(server: URL(string: "http://localhost:3627/"), thisMac: "192.168.0.2", resolve: { _ in nil })
    #expect(local == .init(url: "http://192.168.0.2:3627", replacedHost: "localhost"))
    let mdns = KnobEmberURL.suggest(server: URL(string: "http://mini.local:3627"), thisMac: "192.168.0.2",
                                    resolve: { $0 == "mini.local" ? "192.168.0.5" : nil })
    #expect(mdns == .init(url: "http://192.168.0.5:3627", replacedHost: "mini.local"))
    let lan = KnobEmberURL.suggest(server: URL(string: "http://192.168.0.9:3627/x"), thisMac: "192.168.0.2", resolve: { _ in nil })
    #expect(lan == .init(url: "http://192.168.0.9:3627", replacedHost: nil))
    let none = KnobEmberURL.suggest(server: URL(string: "http://127.0.0.1:3627"), thisMac: nil, resolve: { _ in nil })
    #expect(none == .init(url: "http://127.0.0.1:3627", replacedHost: nil))
    #expect(KnobEmberURL.isPrivateIPv4("10.0.0.1") && KnobEmberURL.isPrivateIPv4("172.20.1.1") && !KnobEmberURL.isPrivateIPv4("8.8.8.8"))
}

final class CountingOpener: KnobLinkOpener, @unchecked Sendable {
    private let lock = NSLock()
    private var _opens = 0
    var opens: Int { lock.withLock { _opens } }
    func open(path: String) async throws -> any KnobLink {
        lock.withLock { _opens += 1 }
        throw KnobLinkError.busy
    }
    func reopen(serialNumber: String, timeout: Duration) async throws -> any KnobLink { throw KnobLinkError.notFound }
}

@MainActor
@Test func knobPortsAreDetectedWithoutOpeningThem() async {
    let port = KnobSerialPort(path: "/dev/cu.fake", vendorID: 0x303A, productID: 0x1001, serialNumber: "3C:DC:75:61:FC:8C")
    let fake = FakeRegistry()
    let opener = CountingOpener()
    let ports = KnobSerialPorts(scanner: { [port] })
    ports.rescan()
    let m = KnobModel(service: KnobService(client: stubbedClient(token: "t") { fake.handle($0) }),
                      ports: ports, opener: opener, debounce: .zero, sleep: { _ in })
    await m.load()
    #expect(m.registeredKnobOnUSB)
    #expect(m.connectedPort == port)
    #expect(opener.opens == 0)
    await m.probePorts()
    #expect(opener.opens == 1)
    #expect(m.portStatus[port.path] == .unavailable)
    await m.probePorts()
    #expect(opener.opens == 1)
    await m.probePorts(retryFailed: true)
    #expect(opener.opens == 2)
    m.portBusy = true
    await m.probePorts(retryFailed: true)
    #expect(opener.opens == 2)
}

@MainActor
@Test func setUpReplacingForgetsTheOldKnob() async {
    let fake = FakeRegistry()
    let m = model(fake)
    await m.load()
    let old = KnobDevice(id: "knob-old", hwID: "000000000001", name: "Old", createdAt: .distantPast)
    await m.didSetUp(m.knob!, replacing: old)
    #expect(fake.log.contains { $0.0 == "DELETE" && $0.1 == "/v1/devices/knob-old" })
}
