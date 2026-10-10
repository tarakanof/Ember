import Foundation
import Testing
@testable import EmberKit

private final class FirmwareServer: @unchecked Sendable {
    private let lock = NSLock()
    private var _log: [String] = []
    private var _versions: [(String, String)] = []
    private var _ota = ""
    private var _devices = #"{"devices":[]}"#
    private var _failing: [String: Int] = [:]
    private var _gate: DispatchSemaphore?
    private var _gatePrefix = "DELETE "
    private var _arrived = false
    private var _features = #"["firmware_delete_keep"]"#
    private var _kept: [String: String] = [:]

    init(versions: [String], knobOn fw: String) {
        _versions = versions.map { ($0, KnobFirmwareImage.release) }
        _ota = Self.ota(fw: fw)
    }

    static func ota(fw: String, target: String? = nil) -> String {
        let t = target.map { #""target":"\#($0)","version":"\#($0)","phase":"offered","# } ?? #""phase":"idle","#
        return #"{"mode":"manual",\#(t)"running":{"fw":"\#(fw)","rollback":true}}"#
    }

    var log: [String] { lock.withLock { _log } }
    var deletes: [String] {
        log.filter { $0.hasPrefix("DELETE ") }.map { String($0.dropFirst("DELETE /v1/firmware/".count).prefix { $0 != "?" }) }
    }
    var arrived: Bool { lock.withLock { _arrived } }

    func set(ota: String) { lock.withLock { _ota = ota } }
    func set(devices: String) { lock.withLock { _devices = devices } }
    func fail(_ path: String, status: Int = 500) { lock.withLock { _failing[path] = status } }
    func heal(_ path: String) { lock.withLock { _ = _failing.removeValue(forKey: path) } }
    func holdFirstDelete() -> DispatchSemaphore { hold("DELETE ") }
    func hold(_ prefix: String) -> DispatchSemaphore {
        let s = DispatchSemaphore(value: 0)
        lock.withLock { _gate = s; _gatePrefix = prefix; _arrived = false }
        return s
    }
    func removeVersion(_ v: String) { lock.withLock { _versions.removeAll { $0.0 == v } } }
    func set(features: String) { lock.withLock { _features = features } }
    func keep(_ version: String, _ reason: String) { lock.withLock { _kept[version] = reason } }

    func handle(_ req: URLRequest) throws -> (HTTPURLResponse, Data) {
        let method = req.httpMethod ?? "GET"
        let url = req.url!
        let key = "\(method) \(url.path)"
        let (failing, ota, devices, versions, features, gate) = lock.withLock {
            _log.append(key + (url.query.map { "?\($0)" } ?? ""))
            var gate: DispatchSemaphore?
            if let g = _gate, key.hasPrefix(_gatePrefix) { gate = g; _gate = nil; _arrived = true }
            return (_failing[url.path], _ota, _devices, _versions, _features, gate)
        }
        gate?.wait()
        if let failing { return (okResponse(url, status: failing), Data(#"{"error":"boom"}"#.utf8)) }
        switch (method, url.path) {
        case ("GET", "/v1/devices/knob-a/ota"):
            return (okResponse(url), Data(ota.utf8))
        case ("GET", "/version"):
            return (okResponse(url), Data(#"{"binary":"ember","version":"dev","features":\#(features)}"#.utf8))
        case ("GET", "/v1/devices"):
            return (okResponse(url), Data(devices.utf8))
        case ("GET", "/v1/firmware"):
            let items = versions.map { v, ch in
                #"{"version":"\#(v)","build":"b\#(v.filter(\.isNumber))","channel":"\#(ch)","elf":true,"idf_ver":"v5.5.5","project":"cinder","sha256":"x","size":1,"uploaded_at":"2026-10-06T12:00:00Z"}"#
            }
            return (okResponse(url), Data("[\(items.joined(separator: ","))]".utf8))
        case ("DELETE", _):
            let version = url.lastPathComponent
            if url.query == "keep=protected", let reason = lock.withLock({ _kept[version] }) {
                return (okResponse(url, status: 409), Data(#"{"error":"\#(reason)"}"#.utf8))
            }
            lock.withLock { _versions.removeAll { "/v1/firmware/\($0.0)" == url.path } }
            return (okResponse(url, status: 204), Data())
        default:
            return (okResponse(url, status: 404), Data(#"{"error":"not found"}"#.utf8))
        }
    }
}

private let versions = ["0.9.39", "0.9.40", "0.9.41", "0.9.42", "0.9.43"]

@MainActor
private func model(_ server: FirmwareServer) async -> KnobOTAModel {
    let service = KnobService(client: stubbedClient(token: "t") { try server.handle($0) })
    let m = KnobOTAModel(service: service)
    m.configure(service: service, device: "knob-a")
    await m.loadStatus()
    await m.loadImages()
    return m
}

@MainActor
private func waitUntil(_ cond: () -> Bool) async throws {
    for _ in 0..<500 where !cond() { try await Task.sleep(for: .milliseconds(5)) }
    try #require(cond())
}

@MainActor
@Test func deleteOldBuildsDeletesTheConfirmedListAndReloadsOnce() async {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    let m = await model(server)
    let confirmed = m.oldBuilds(otherKnobs: [])
    #expect(confirmed.map(\.version) == ["0.9.39", "0.9.40", "0.9.41", "0.9.42"])
    let lists = server.log.filter { $0 == "GET /v1/firmware" }.count
    #expect(await m.deleteOldBuilds(confirmed))
    #expect(server.deletes == ["0.9.39", "0.9.40", "0.9.41", "0.9.42"])
    #expect(server.log.filter { $0 == "GET /v1/firmware" }.count == lists + 2)
    #expect(m.images.map(\.version) == ["0.9.43"])
    #expect(!m.running.contains(.delete) && m.errors[.delete] == nil)
}

@MainActor
@Test func deleteOldBuildsSkipsWhatChangedWhileTheDialogWasOpen() async {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.39")
    let m = await model(server)
    let confirmed = m.oldBuilds(otherKnobs: [])
    #expect(confirmed.map(\.version) == ["0.9.40", "0.9.41", "0.9.42"])
    server.set(ota: FirmwareServer.ota(fw: "0.9.41"))
    #expect(await m.deleteOldBuilds(confirmed))
    #expect(server.deletes == ["0.9.40", "0.9.42"])
}

@MainActor
@Test func deleteOldBuildsRechecksBetweenDeletes() async throws {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.39")
    let m = await model(server)
    let confirmed = m.oldBuilds(otherKnobs: [])
    let gate = server.holdFirstDelete()
    let run = Task { await m.deleteOldBuilds(confirmed) }
    try await waitUntil { server.arrived }
    server.set(ota: FirmwareServer.ota(fw: "0.9.39", target: "0.9.42"))
    let loaded = Task { await m.loadStatus() }
    try await waitUntil { m.status?.target == "0.9.42" }
    gate.signal()
    await loaded.value
    #expect(await run.value)
    #expect(server.deletes == ["0.9.40", "0.9.41"])
}

@MainActor
@Test func deleteOldBuildsAbortsWhenARefreshFails() async {
    for path in ["/v1/devices/knob-a/ota", "/v1/firmware", "/v1/devices"] {
        let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
        let m = await model(server)
        let confirmed = m.oldBuilds(otherKnobs: [])
        server.fail(path)
        #expect(await m.deleteOldBuilds(confirmed) == false)
        #expect(server.deletes.isEmpty)
        #expect(m.errors[.delete] != nil)
        #expect(!m.running.contains(.delete))
    }
}

@MainActor
@Test func deleteOldBuildsKeepsWhatOtherKnobsRun() async {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    server.set(devices: #"{"devices":[{"id":"knob-a","kind":"cinder-knob","hw_id":"a","name":"A","created_at":"2026-10-04T10:00:00Z","config_version":1,"rotation_pending":false,"rotated_at":null,"last_checkin":{"seen_at":"2026-10-06T12:00:00Z","fw":"0.9.43","ip":"","rssi":0,"heap_internal_free":0,"heap_internal_largest":0,"uptime_s":0,"applied_version":1}},{"id":"knob-b","kind":"cinder-knob","hw_id":"b","name":"B","created_at":"2026-10-04T10:00:00Z","config_version":1,"rotation_pending":false,"rotated_at":null,"last_checkin":{"seen_at":"2026-10-06T12:00:00Z","fw":"0.9.40","ip":"","rssi":0,"heap_internal_free":0,"heap_internal_largest":0,"uptime_s":0,"applied_version":1}}]}"#)
    let m = await model(server)
    #expect(await m.deleteOldBuilds(m.oldBuilds(otherKnobs: [])))
    #expect(server.deletes == ["0.9.39", "0.9.41", "0.9.42"])
}

@MainActor
@Test func deleteOldBuildsStopsWhenTheServerChanges() async throws {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    let m = await model(server)
    let confirmed = m.oldBuilds(otherKnobs: [])
    let other = FirmwareServer(versions: versions, knobOn: "0.9.43")
    let otherService = KnobService(client: stubbedClient(token: "u") { try other.handle($0) })
    let gate = server.holdFirstDelete()
    let run = Task { await m.deleteOldBuilds(confirmed) }
    try await waitUntil { server.arrived }
    m.configure(service: otherService, device: "knob-a")
    gate.signal()
    #expect(await run.value == false)
    #expect(server.deletes == ["0.9.39"])
    #expect(other.log.isEmpty)
    #expect(m.images.isEmpty && m.failedDeletes.isEmpty)
}

@MainActor
@Test func deleteOldBuildsSendsTheGuardAndTreatsAKeptAnswerAsKept() async {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    server.keep("0.9.40", "firmware_kept_in_use")
    server.keep("0.9.41", "firmware_kept_newest")
    let m = await model(server)
    let confirmed = m.oldBuilds(otherKnobs: [])
    #expect(await m.deleteOldBuilds(confirmed))
    #expect(server.log.filter { $0.hasPrefix("DELETE ") }.allSatisfy { $0.hasSuffix("?keep=protected") })
    #expect(server.deletes == ["0.9.39", "0.9.40", "0.9.41", "0.9.42"])
    let row = { (v: String) in confirmed.first { $0.version == v }! }
    #expect(m.kept(row("0.9.40")) == .inUse && m.kept(row("0.9.41")) == .newest && m.kept(row("0.9.39")) == nil)
    #expect(m.deleteError(for: row("0.9.40")) == nil && m.deleteError(for: row("0.9.41")) == nil)
    #expect(m.errors[.delete] == nil)
}

@MainActor
@Test func singleDeleteStaysUnguarded() async throws {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    let m = await model(server)
    #expect(await m.delete(try #require(m.images.first { $0.version == "0.9.43" })))
    #expect(server.log.contains("DELETE /v1/firmware/0.9.43"))
}

@MainActor
@Test func deleteOldBuildsRefusesAServerWithoutTheGuard() async {
    for features in ["null", "[]", #"["other"]"#] {
        let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
        server.set(features: features)
        let m = await model(server)
        #expect(await m.deleteOldBuilds(m.oldBuilds(otherKnobs: [])) == false)
        #expect(server.deletes.isEmpty)
        #expect(m.errors[.delete] == .rejected(KnobOTAError.serverCantKeep))
    }
}

@MainActor
@Test func aListFromTheOldServerNeverLandsAfterASwitch() async throws {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    let service = KnobService(client: stubbedClient(token: "t") { try server.handle($0) })
    let m = KnobOTAModel(service: service)
    m.configure(service: service, device: "knob-a")
    let gate = server.hold("GET /v1/firmware")
    let load = Task { await m.loadImages() }
    try await waitUntil { server.arrived }
    let other = FirmwareServer(versions: ["0.9.50"], knobOn: "0.9.50")
    m.configure(service: KnobService(client: stubbedClient(token: "u") { try other.handle($0) }), device: "knob-a")
    gate.signal()
    await load.value
    #expect(m.images.isEmpty && !m.imagesLoaded)
    await m.loadImages()
    #expect(m.images.map(\.version) == ["0.9.50"])
}

@MainActor
@Test func anOlderListNeverOverwritesANewerOne() async throws {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    let m = await model(server)
    let gate = server.hold("GET /v1/firmware")
    let slow = Task { await m.loadImages() }
    try await waitUntil { server.arrived }
    server.removeVersion("0.9.39")
    await m.loadImages()
    #expect(m.images.count == 4)
    gate.signal()
    await slow.value
    #expect(m.images.count == 4)
}

@MainActor
@Test func aSwitchClearsTheOldRunsDeleteFlag() async throws {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    let m = await model(server)
    let gate = server.holdFirstDelete()
    let run = Task { await m.deleteOldBuilds(m.oldBuilds(otherKnobs: [])) }
    try await waitUntil { server.arrived }
    #expect(m.running.contains(.delete))
    let other = FirmwareServer(versions: versions, knobOn: "0.9.43")
    m.configure(service: KnobService(client: stubbedClient(token: "u") { try other.handle($0) }), device: "knob-a")
    #expect(!m.running.contains(.delete))
    gate.signal()
    _ = await run.value
    #expect(!m.running.contains(.delete))
}

@MainActor
@Test func anOlderFailureNeverMarksTheStoreUnsupported() async throws {
    let server = FirmwareServer(versions: versions, knobOn: "0.9.43")
    let m = await model(server)
    server.fail("/v1/firmware", status: 404)
    let gate = server.hold("GET /v1/firmware")
    let slow = Task { await m.loadImages() }
    try await waitUntil { server.arrived }
    server.heal("/v1/firmware")
    await m.loadImages()
    gate.signal()
    await slow.value
    #expect(!m.unsupported && m.images.count == 5)
}

@Test func keptAnswersAreReadFromTheirCodes() {
    func kept(_ body: String) -> KnobFirmwareKept? { KnobFirmwareKept(APIError.http(status: 409, body: body)) }
    #expect(kept(#"{"error":"firmware_kept_in_use"}"#) == .inUse)
    #expect(kept(#"{"error":"firmware_kept_newest"}"#) == .newest)
    #expect(kept(#"{"error":"this version is a device's update target"}"#) == .inUse)
    #expect(kept(#"{"error":"boom"}"#) == nil)
    #expect(KnobFirmwareKept(APIError.http(status: 500, body: "firmware_kept_newest")) == nil)
}
