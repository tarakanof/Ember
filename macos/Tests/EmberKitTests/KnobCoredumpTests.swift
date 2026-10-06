import Testing
import Foundation
@testable import EmberKit

private let coredumpDevice = #"{"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"3cdc7561fc8c","name":"Desk knob","created_at":"2026-10-04T10:00:00Z","config_version":3,"rotation_pending":false,"rotated_at":null,"last_checkin":null}"#
private let coredumpList = #"[{"id":"1a2b3c4d","size":65536,"fw":"0.9.14","received_at":"2026-10-06T10:00:00Z","reason":"panic","task":"ember","pc":"0x4201a2b3"},{"id":"0badc0de","size":4096,"fw":"","received_at":"2026-10-05T09:00:00Z","reason":"","task":"","pc":""}]"#

private final class CoredumpServer: @unchecked Sendable {
    private let lock = NSLock()
    private var _log: [(String, String)] = []
    var list = coredumpList
    var dumpStatus = 200
    var log: [(String, String)] { lock.withLock { _log } }

    func handle(_ req: URLRequest) -> (HTTPURLResponse, Data) {
        let method = req.httpMethod ?? "GET"
        let path = req.url!.path
        lock.withLock { _log.append((method, path)) }
        #expect(req.value(forHTTPHeaderField: "Authorization") == "Bearer t")
        switch (method, path) {
        case ("GET", "/v1/devices"):
            return (okResponse(req.url!), Data(#"{"devices":[\#(coredumpDevice)]}"#.utf8))
        case ("GET", "/v1/devices/knob-61fc8c/config"):
            return (okResponse(req.url!), Data(#"{"home":"bot","poll_ms":2000}"#.utf8))
        case ("GET", "/v1/devices/knob-61fc8c/coredumps"):
            return (okResponse(req.url!), Data(list.utf8))
        case ("GET", "/v1/devices/knob-61fc8c/coredumps/1a2b3c4d"):
            return (okResponse(req.url!, status: dumpStatus), Data([0xde, 0xad, 0xbe, 0xef]))
        case ("DELETE", "/v1/devices/knob-61fc8c/coredumps/1a2b3c4d"):
            return (okResponse(req.url!, status: 204), Data())
        default:
            return (okResponse(req.url!, status: 404), Data(#"{"error":"not found"}"#.utf8))
        }
    }
}

@Test func knobCrashDecodesTheDumpIDAndSize() throws {
    let c = try JSONDecoder().decode(KnobCrash.self, from: Data(#"{"id":"1a2b3c4d","size":65536,"pc":"0x4201a2b3","reason":"panic","task":"ember"}"#.utf8))
    #expect(c == KnobCrash(id: "1a2b3c4d", size: 65536, pc: "0x4201a2b3", reason: "panic", task: "ember"))
    let old = try JSONDecoder().decode(KnobCrash.self, from: Data(#"{"reason":"panic"}"#.utf8))
    #expect(old.id == nil && old.size == nil)
}

@Test func knobCoredumpListDecodes() throws {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let list = try d.decode([KnobCoredump].self, from: Data(coredumpList.utf8))
    #expect(list.count == 2)
    #expect(list[0] == KnobCoredump(id: "1a2b3c4d", size: 65536, fw: "0.9.14",
                                    receivedAt: Date(timeIntervalSince1970: 1_791_280_800),
                                    reason: "panic", task: "ember", pc: "0x4201a2b3"))
    #expect(list[1].fw.isEmpty && list[1].reason.isEmpty)
    let sparse = try d.decode(KnobCoredump.self, from: Data(#"{"id":"0badc0de","size":8,"received_at":"2026-10-05T09:00:00Z"}"#.utf8))
    #expect(sparse.fw.isEmpty && sparse.pc.isEmpty)
}

@Test func knobCoredumpFilenameMatchesTheServer() {
    let at = Date(timeIntervalSince1970: 0)
    #expect(KnobCoredump(id: "1a2b3c4d", size: 1, fw: "0.9.14", receivedAt: at).filename(device: "knob-61fc8c")
            == "knob-61fc8c-0.9.14-1a2b3c4d.bin")
    #expect(KnobCoredump(id: "1a2b3c4d", size: 1, fw: "", receivedAt: at).filename(device: "knob-61fc8c")
            == "knob-61fc8c-unknown-1a2b3c4d.bin")
    #expect(KnobCoredump(id: "1a2b3c4d", size: 1, fw: "0.9 dev/ü", receivedAt: at).filename(device: "knob-61fc8c")
            == "knob-61fc8c-0.9_dev__-1a2b3c4d.bin")
}

@Test func knobServiceBuildsTheCoredumpRequests() async throws {
    let server = CoredumpServer()
    let svc = KnobService(client: stubbedClient(token: "t") { server.handle($0) })
    let list = try await svc.coredumps(id: "knob-61fc8c")
    #expect(list.map(\.id) == ["1a2b3c4d", "0badc0de"])
    let bytes = try await svc.coredump(id: "knob-61fc8c", dump: "1a2b3c4d")
    #expect(bytes == Data([0xde, 0xad, 0xbe, 0xef]))
    try await svc.deleteCoredump(id: "knob-61fc8c", dump: "1a2b3c4d")
    #expect(server.log.map { "\($0.0) \($0.1)" } == [
        "GET /v1/devices/knob-61fc8c/coredumps",
        "GET /v1/devices/knob-61fc8c/coredumps/1a2b3c4d",
        "DELETE /v1/devices/knob-61fc8c/coredumps/1a2b3c4d",
    ])
}

@MainActor
private func coredumpModel(_ server: CoredumpServer) -> KnobModel {
    KnobModel(service: KnobService(client: stubbedClient(token: "t") { server.handle($0) }),
              ports: KnobSerialPorts(scanner: { [] }), opener: FakeOpener(FakeKnob()),
              debounce: .zero, sleep: { _ in })
}

@MainActor
@Test func knobModelLoadsTheStoredCoredumps() async {
    let server = CoredumpServer()
    let m = coredumpModel(server)
    await m.load()
    #expect(m.coredumps.map(\.id) == ["1a2b3c4d", "0badc0de"])
    #expect(m.coredump(for: KnobCrash(id: "1a2b3c4d"))?.fw == "0.9.14")
    #expect(m.coredump(for: KnobCrash(id: "ffffffff")) == nil)
    #expect(m.coredump(for: KnobCrash(reason: "panic")) == nil)
}

@MainActor
@Test func knobModelDownloadsACoredumpAndReportsFailure() async throws {
    let server = CoredumpServer()
    let m = coredumpModel(server)
    await m.load()
    let dump = try #require(m.coredumps.first)
    #expect(await m.coredumpData(dump) == Data([0xde, 0xad, 0xbe, 0xef]))
    #expect(m.actionErrors[.coredump] == nil)
    server.dumpStatus = 404
    #expect(await m.coredumpData(dump) == nil)
    #expect(m.actionErrors[.coredump] != nil)
}

@MainActor
@Test func knobModelKeepsNoCoredumpsWhenTheServerHasNone() async {
    let server = CoredumpServer()
    server.list = "[]"
    let m = coredumpModel(server)
    await m.load()
    #expect(m.isLoaded && m.coredumps.isEmpty)
}
