import Testing
import Foundation
@testable import EmberKit

private func otaGolden(_ name: String) throws -> Data {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("cmd/ember/testdata/dashboard/\(name).json")
    return try Data(contentsOf: url)
}

private func decode<T: Decodable>(_ type: T.Type, _ data: Data) throws -> T {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return try d.decode(T.self, from: data)
}

@Test func otaStatusDecodesTheIdleGolden() throws {
    let s = try decode(KnobOTAStatus.self, otaGolden("knob_ota_idle"))
    #expect(s.mode == .manual && s.phase == .idle && s.target == nil)
    #expect(s.available == "0.9.15-rc1")
    #expect(s.running == KnobOTAStatus.Running(fw: "0.9.13", build: "77aa01ff", slot: 0, image: "valid", rollback: true))
    #expect(s.canRollBack && !s.isBusy && s.progress == nil && s.blocked.isEmpty)
}

@Test func otaStatusDecodesTheDownloadingGolden() throws {
    let s = try decode(KnobOTAStatus.self, otaGolden("knob_ota_downloading"))
    #expect(s.phase == .downloading && s.target == "0.9.14" && s.from == "0.9.13")
    #expect(s.bytes == 30000 && s.size == 71680 && s.progressPct == 41)
    #expect(s.progress == 0.41)
    #expect(s.isBusy && s.phase.isInProgress)
    #expect(s.startedAt == (try Date("2026-10-06T12:00:00Z", strategy: .iso8601)))
}

@Test func otaStatusDecodesTheRolledBackGolden() throws {
    let s = try decode(KnobOTAStatus.self, otaGolden("knob_ota_rolled_back"))
    #expect(s.phase == .rolledBack && s.phase.isFailure && !s.phase.isInProgress)
    #expect(s.error == "no_checkin" && s.blocked == ["0.9.14"])
    #expect(s.finishedAt != nil)
}

@Test func firmwareListDecodesTheGolden() throws {
    let list = try decode([KnobFirmwareImage].self, otaGolden("firmware_list"))
    #expect(list.map(\.version) == ["0.9.15-rc1", "0.9.14"])
    #expect(list[1].isRelease && !list[0].isRelease)
    #expect(list[1].idfVer == "v5.5.5" && list[1].size == 71680 && !list[1].elf)
    #expect(list[1].elfFilename == "cinder-0.9.14.elf")
}

@Test func otaStatusToleratesUnknownValues() throws {
    let s = try decode(KnobOTAStatus.self, Data(#"{"mode":"sometimes","phase":"teleporting","waiting_for":"moon"}"#.utf8))
    #expect(s.mode == .manual && s.phase == .idle && s.waitingFor == nil && s.blocked.isEmpty)
}

@Test func checkinDecodesTheOTAReportAndCrashELF() throws {
    let c = try decode(KnobCheckin.self, Data(#"{"seen_at":"2026-10-06T12:00:00Z","fw":"0.9.14","ip":"","rssi":0,"heap_internal_free":0,"heap_internal_largest":0,"uptime_s":0,"applied_version":1,"fw_build":"a1b2c3d4","ota":{"image":"pending_verify","phase":"idle","rollback":true,"slot":1},"diag":{"crash":{"elf":"0badc0de","id":"1a2b3c4d","size":4096}}}"#.utf8))
    #expect(c.fwBuild == "a1b2c3d4")
    #expect(c.ota == KnobOTAReport(image: "pending_verify", phase: "idle", rollback: true, slot: 1))
    #expect(c.diag?.crash?.elf == "0badc0de")
    let dump = try decode(KnobCoredump.self, Data(#"{"id":"1a2b3c4d","size":8,"received_at":"2026-10-05T09:00:00Z","elf":"0badc0de"}"#.utf8))
    #expect(dump.elf == "0badc0de")
}

@Test func otaErrorLabels() {
    #expect(KnobOTAError.label("no_checkin") == "the knob could not reach Ember after the update")
    #expect(KnobOTAError.label("http_409") == "Ember answered HTTP 409")
    #expect(KnobOTAError.label("weird") == "weird")
    #expect(KnobOTAError.label("refused") == "the knob refused this image after it failed there before")
    #expect(KnobOTAError.label("not_started") == "the knob never started the download")
}

@Test(arguments: [
    ("reset_waiting", "the knob restarted while the update was waiting to install"),
    ("health_display", "the display link check did not pass after the update"),
    ("health_render", "the screen did not draw after the update"),
    ("health_heap", "memory ran low after the update"),
    ("health_stack", "a task ran low on stack after the update"),
])
func otaHealthAndResetLabels(code: String, label: String) {
    #expect(KnobOTAError.label(code) == label)
}

@Test func elfIsFoundNextToTheBinary() {
    let bin = URL(fileURLWithPath: "/tmp/build/cinder.bin")
    #expect(KnobOTAModel.elfURL(besides: bin, exists: { $0.lastPathComponent == "cinder.elf" })?.path == "/tmp/build/cinder.elf")
    let named = URL(fileURLWithPath: "/tmp/rel/cinder-0.9.14.bin")
    #expect(KnobOTAModel.elfURL(besides: named, exists: { _ in true })?.lastPathComponent == "cinder-0.9.14.elf")
    #expect(KnobOTAModel.elfURL(besides: bin, exists: { _ in false }) == nil)
}

private let otaDevice = #"{"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"3cdc7561fc8c","name":"Desk knob","created_at":"2026-10-04T10:00:00Z","config_version":3,"rotation_pending":false,"rotated_at":null,"last_checkin":null}"#

private final class OTAServer: @unchecked Sendable {
    private let lock = NSLock()
    private var _log: [String] = []
    private var _bodies: [String: Data] = [:]
    var log: [String] { lock.withLock { _log } }
    func body(_ key: String) -> Data? { lock.withLock { _bodies[key] } }
    private var _putReply: (Int, String)?
    var putReply: (Int, String)? {
        get { lock.withLock { _putReply } }
        set { lock.withLock { _putReply = newValue } }
    }

    func handle(_ req: URLRequest) throws -> (HTTPURLResponse, Data) {
        let method = req.httpMethod ?? "GET"
        let url = req.url!
        let key = "\(method) \(url.path)\(url.query.map { "?\($0)" } ?? "")"
        let body = req.httpBody ?? req.httpBodyStreamData() ?? Data()
        lock.withLock { _log.append(key); _bodies[key] = body }
        #expect(req.value(forHTTPHeaderField: "Authorization") == "Bearer t")
        switch (method, url.path) {
        case ("GET", "/v1/devices"):
            return (okResponse(url), Data(#"{"devices":[\#(otaDevice)]}"#.utf8))
        case ("GET", "/v1/devices/knob-61fc8c/config"):
            return (okResponse(url), Data(#"{"home":"bot","poll_ms":2000}"#.utf8))
        case ("GET", "/v1/devices/knob-61fc8c/coredumps"):
            return (okResponse(url), Data("[]".utf8))
        case ("GET", "/v1/devices/knob-61fc8c/ota"):
            return (okResponse(url), try otaGolden("knob_ota_idle"))
        case ("PUT", "/v1/devices/knob-61fc8c/ota"):
            if let (status, reply) = putReply { return (okResponse(url, status: status), Data(reply.utf8)) }
            return (okResponse(url), try otaGolden("knob_ota_downloading"))
        case ("GET", "/v1/firmware"):
            return (okResponse(url), try otaGolden("firmware_list"))
        case ("POST", "/v1/firmware"):
            #expect(req.value(forHTTPHeaderField: "Content-Type") == "application/octet-stream")
            let first = String(data: try otaGolden("firmware_list"), encoding: .utf8)!
            let image = try JSONSerialization.jsonObject(with: Data(first.utf8)) as! [[String: Any]]
            return (okResponse(url, status: 201), try JSONSerialization.data(withJSONObject: image[1]))
        case ("PUT", "/v1/firmware/0.9.14/elf"):
            return (okResponse(url, status: 204), Data())
        case ("PATCH", "/v1/firmware/0.9.15-rc1"), ("PATCH", "/v1/firmware/0.9.14"):
            let channel = (try JSONSerialization.jsonObject(with: body) as? [String: String])?["channel"] ?? "?"
            let version = url.lastPathComponent
            return (okResponse(url), Data(#"{"build":"8dcd6329","channel":"\#(channel)","elf":false,"idf_ver":"v5.5.5","project":"cinder","sha256":"x","size":1,"uploaded_at":"2026-10-06T12:00:00Z","version":"\#(version)"}"#.utf8))
        case ("DELETE", "/v1/firmware/0.9.15-rc1"):
            return (okResponse(url, status: 204), Data())
        case ("GET", "/v1/firmware/by-build/42f7f65e/elf"):
            return (okResponse(url), Data([0x7f, 0x45, 0x4c, 0x46]))
        default:
            return (okResponse(url, status: 404), Data(#"{"error":"not found"}"#.utf8))
        }
    }
}

@MainActor
private func otaModel(_ server: OTAServer) -> KnobModel {
    KnobModel(service: KnobService(client: stubbedClient(token: "t") { try server.handle($0) }),
              ports: KnobSerialPorts(scanner: { [] }), opener: FakeOpener(FakeKnob()),
              debounce: .zero, sleep: { _ in })
}

@MainActor
@Test func knobModelLoadsTheOTAStatusAndImages() async {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    #expect(m.ota.status?.available == "0.9.15-rc1")
    #expect(m.ota.images.map(\.version) == ["0.9.15-rc1", "0.9.14"])
    #expect(m.ota.imagesLoaded && !m.ota.unsupported)
    #expect(m.ota.pollInterval == .seconds(15))
}

@MainActor
@Test func otaUpdateSendsTheTargetAndShowsProgress() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    #expect(await m.ota.update(to: "0.9.14"))
    #expect(m.ota.status?.phase == .downloading)
    #expect(m.ota.pollInterval == .seconds(1))
    let sent = try #require(server.body("PUT /v1/devices/knob-61fc8c/ota"))
    #expect(String(data: sent, encoding: .utf8) == #"{"target":"0.9.14"}"#)
    #expect(await m.ota.retry())
    #expect(String(data: try #require(server.body("PUT /v1/devices/knob-61fc8c/ota")), encoding: .utf8) == #"{"retry":true}"#)
    #expect(await m.ota.setMode(.auto))
    #expect(String(data: try #require(server.body("PUT /v1/devices/knob-61fc8c/ota")), encoding: .utf8) == #"{"mode":"auto"}"#)
}

@MainActor
@Test func otaUploadSendsTheImageThenItsELF() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("ota-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: dir) }
    let bin = dir.appendingPathComponent("cinder.bin")
    let elf = dir.appendingPathComponent("cinder.elf")
    try Data([0xE9, 1, 2, 3]).write(to: bin)
    try Data([0x7f, 0x45]).write(to: elf)
    let image = await m.ota.upload(binary: bin, channel: KnobFirmwareImage.test, elf: KnobOTAModel.elfURL(besides: bin))
    #expect(image?.version == "0.9.14")
    #expect(m.ota.errors[.upload] == nil && m.ota.uploadProgress == nil)
    #expect(server.log.contains("POST /v1/firmware?channel=test"))
    #expect(server.log.contains("PUT /v1/firmware/0.9.14/elf"))
}

@MainActor
@Test func otaPromoteDeleteAndELFByBuild() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    let rc = try #require(m.ota.images.first)
    #expect(await m.ota.setChannel(rc, to: KnobFirmwareImage.release))
    let sent = try #require(server.body("PATCH /v1/firmware/0.9.15-rc1"))
    #expect(String(data: sent, encoding: .utf8) == #"{"channel":"release"}"#)
    #expect(await m.ota.delete(rc))
    #expect(server.log.contains("DELETE /v1/firmware/0.9.15-rc1"))
    #expect(await m.ota.elfData(build: "42f7f65e") == Data([0x7f, 0x45, 0x4c, 0x46]))
    #expect(await m.ota.elfData(build: "00000000") == nil)
    #expect(m.ota.elfImage(build: "42f7f65e") == nil)
    #expect(m.ota.errors[.elf] != nil)
}

@MainActor
@Test func otaReportsAnOldServer() async {
    let client = stubbedClient(token: "t") { req in
        if req.url!.path == "/v1/devices" {
            return (okResponse(req.url!), Data(#"{"devices":[\#(otaDevice)]}"#.utf8))
        }
        return (okResponse(req.url!, status: 404), Data("404 page not found".utf8))
    }
    let m = KnobModel(service: KnobService(client: client), ports: KnobSerialPorts(scanner: { [] }),
                      opener: FakeOpener(FakeKnob()), debounce: .zero, sleep: { _ in })
    await m.load()
    #expect(m.ota.unsupported && m.ota.status == nil)
}

@Test func otaStatusCarriesTheAttemptVersion() throws {
    #expect(try decode(KnobOTAStatus.self, otaGolden("knob_ota_idle")).version == nil)
    #expect(try decode(KnobOTAStatus.self, otaGolden("knob_ota_downloading")).version == "0.9.14")
    let auto = try decode(KnobOTAStatus.self, Data(#"{"mode":"auto","target":null,"version":"0.9.15","phase":"failed","error":"net"}"#.utf8))
    #expect(auto.attemptVersion == "0.9.15")
    #expect(KnobOTAStatus(target: "0.9.14", phase: .failed).attemptVersion == "0.9.14")
}

@Test func otaPollsFastOnlyWhileTheKnobIsBusyWithTheImage() {
    let fast: [KnobOTAPhase] = [.downloading, .installing, .restarting]
    for phase in [KnobOTAPhase.idle, .offered, .downloading, .installing, .restarting, .verifying, .done, .failed, .rolledBack] {
        #expect(phase.pollsFast == fast.contains(phase), "\(phase)")
    }
}

@MainActor
@Test func otaPollIntervalFollowsThePhase() async {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    #expect(m.ota.pollInterval == .seconds(15))
    _ = await m.ota.update(to: "0.9.14")
    #expect(m.ota.status?.phase == .downloading && m.ota.pollInterval == .seconds(1))
}

@Test func otaHidesUpdateForTheVersionThatJustFailed() {
    let failed = KnobOTAStatus(target: "0.9.14", phase: .rolledBack, available: "0.9.14", version: "0.9.14")
    #expect(failed.updateVersion == nil)
    let newer = KnobOTAStatus(phase: .failed, available: "0.9.16", version: "0.9.15")
    #expect(newer.updateVersion == "0.9.16")
    #expect(KnobOTAStatus(phase: .downloading, available: "0.9.16").updateVersion == nil)
    #expect(KnobOTAStatus(phase: .idle, available: "0.9.16").updateVersion == "0.9.16")
}

@MainActor
@Test func otaKnowsWhichImageTheKnobRuns() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    let images = m.ota.images
    #expect(m.ota.runsOnKnob(try #require(images.first { $0.version == "0.9.14" })) == false)
    _ = await m.ota.update(to: "0.9.14")
    #expect(m.ota.status?.running?.fw == "0.9.13")
    let running = KnobFirmwareImage(build: "77aa01ff", channel: "release", elf: true, idfVer: "v5.5.5",
                                    sha256: "x", size: 1, uploadedAt: .now, version: "0.9.13")
    #expect(m.ota.runsOnKnob(running))
}

@MainActor
@Test func otaChannelCanGoBackToTest() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    let release = try #require(m.ota.images.first { $0.version == "0.9.14" })
    #expect(release.isRelease)
    #expect(await m.ota.setChannel(release, to: KnobFirmwareImage.test))
    let sent = try #require(server.body("PATCH /v1/firmware/0.9.14"))
    #expect(String(data: sent, encoding: .utf8) == #"{"channel":"test"}"#)
    #expect(m.ota.errors[.channel] == nil)
    #expect(await m.ota.setChannel(release, to: "beta") == false)
    #expect(server.log.filter { $0 == "PATCH /v1/firmware/0.9.14" }.count == 1)
}

@MainActor
@Test func otaSkipsAChannelChangeToTheSameChannel() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    let release = try #require(m.ota.images.first { $0.version == "0.9.14" })
    #expect(await m.ota.setChannel(release, to: KnobFirmwareImage.release))
    #expect(!server.log.contains("PATCH /v1/firmware/0.9.14"))
}

private func image(_ version: String, build: String) -> KnobFirmwareImage {
    KnobFirmwareImage(build: build, channel: KnobFirmwareImage.test, elf: false, idfVer: "v5.5.5",
                      sha256: "x", size: 1, uploadedAt: .now, version: version)
}

@Test func otaInstallsAnyStoredBuildButTheRunningOne() {
    let running = KnobOTAStatus.Running(fw: "0.9.16", build: "30887ef8", slot: 0, image: "valid", rollback: true)
    let idle = KnobOTAStatus(running: running, available: "0.9.17")
    #expect(idle.canInstall(image("0.9.17", build: "aa11bb22")))
    #expect(idle.canInstall(image("0.9.15", build: "cc33dd44")))
    #expect(idle.canInstall(image("0.9.16", build: "ee55ff66")))
    #expect(!idle.canInstall(image("0.9.16", build: "30887ef8")))
    let noBuild = KnobOTAStatus(running: .init(fw: "0.9.16", rollback: true))
    #expect(!noBuild.canInstall(image("0.9.16", build: "30887ef8")))
    #expect(noBuild.canInstall(image("0.9.17", build: "aa11bb22")))
    let busy = KnobOTAStatus(target: "0.9.17", phase: .downloading, running: running, version: "0.9.17")
    #expect(!busy.canInstall(image("0.9.15", build: "cc33dd44")))
    #expect(!KnobOTAStatus(running: nil).canInstall(image("0.9.17", build: "aa11bb22")))
}

@MainActor
@Test func otaInstallTargetsTheImage() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    let old = try #require(m.ota.images.first { $0.version == "0.9.14" })
    #expect(await m.ota.install(old))
    let sent = try #require(server.body("PUT /v1/devices/knob-61fc8c/ota"))
    #expect(String(data: sent, encoding: .utf8) == #"{"target":"0.9.14"}"#)
    #expect(m.ota.status?.phase == .downloading)
}

@Test func otaMatchesTheRunningImageByBuild() {
    let running = KnobOTAStatus.Running(fw: "0.9.16", build: "30887ef8", slot: 0, image: "valid", rollback: true)
    let status = KnobOTAStatus(running: running)
    #expect(status.runs(image("0.9.16", build: "30887ef8")))
    #expect(!status.runs(image("0.9.16", build: "ee55ff66")))
    #expect(!status.runs(image("0.9.17", build: "aa11bb22")))
    let noBuild = KnobOTAStatus(running: .init(fw: "0.9.16", rollback: true))
    #expect(noBuild.runs(image("0.9.16", build: "ee55ff66")))
    #expect(!KnobOTAStatus().runs(image("0.9.16", build: "30887ef8")))
}

@MainActor
@Test func otaRunsOnKnobIgnoresAnotherBuildOfTheSameVersion() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    #expect(m.ota.runsOnKnob(image("0.9.13", build: "77aa01ff")))
    #expect(!m.ota.runsOnKnob(image("0.9.13", build: "0badf00d")))
}

@Test func otaOnlyPromotionNeedsConfirmation() {
    #expect(KnobFirmwareImage.needsConfirmation(from: KnobFirmwareImage.test, to: KnobFirmwareImage.release))
    #expect(!KnobFirmwareImage.needsConfirmation(from: KnobFirmwareImage.release, to: KnobFirmwareImage.test))
    #expect(!KnobFirmwareImage.needsConfirmation(from: KnobFirmwareImage.release, to: KnobFirmwareImage.release))
}

@MainActor
@Test func otaFailedInstallReloadsTheStatusAndExplainsTheRefusal() async throws {
    let cases: [(Int, String, FeedError)] = [
        (409, #"{"error":"ota_in_progress"}"#, .rejected(KnobOTAError.inProgress)),
        (409, #"{"error":"no_rollback_bootloader"}"#, .rejected(KnobOTAError.noRollback)),
        (400, #"{"error":"bad device body: unknown firmware version"}"#, .rejected(KnobOTAError.unknownImage)),
    ]
    for (status, reply, want) in cases {
        let server = OTAServer()
        let m = otaModel(server)
        await m.load()
        let old = try #require(m.ota.images.first { $0.version == "0.9.14" })
        server.putReply = (status, reply)
        let before = server.log.filter { $0 == "GET /v1/devices/knob-61fc8c/ota" }.count
        #expect(await m.ota.install(old) == false)
        #expect(m.ota.errors[.install] == want)
        #expect(m.ota.errors[.update] == nil)
        #expect(server.log.filter { $0 == "GET /v1/devices/knob-61fc8c/ota" }.count == before + 1)
        #expect(server.log.last == "GET /v1/devices/knob-61fc8c/ota")
    }
}

@MainActor
@Test func otaFailedUpdateFromTheStatusRowAlsoReloads() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    server.putReply = (409, #"{"error":"ota_in_progress"}"#)
    #expect(await m.ota.update(to: "0.9.14") == false)
    #expect(m.ota.errors[.update] == .rejected(KnobOTAError.inProgress))
    #expect(m.ota.errors[.install] == nil)
    #expect(server.log.last == "GET /v1/devices/knob-61fc8c/ota")
    server.putReply = (500, #"{"error":"boom"}"#)
    #expect(await m.ota.update(to: "0.9.14") == false)
    if case .server? = m.ota.errors[.update] {} else { Issue.record("want a server error, got \(String(describing: m.ota.errors[.update]))") }
}

@MainActor
@Test func otaDeleteOfAnImageAlreadyGoneCountsAsDeleted() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    let old = try #require(m.ota.images.first { $0.version == "0.9.14" })
    let lists = server.log.filter { $0 == "GET /v1/firmware" }.count
    #expect(await m.ota.delete(old))
    #expect(server.log.contains("DELETE /v1/firmware/0.9.14"))
    #expect(m.ota.errors[.delete] == nil)
    #expect(server.log.filter { $0 == "GET /v1/firmware" }.count == lists + 1)
}

private final class Ticks: @unchecked Sendable {
    private let lock = NSLock()
    private var n = 0
    let limit: Int
    init(_ limit: Int) { self.limit = limit }
    var count: Int { lock.withLock { n } }
    func tick() throws {
        try lock.withLock {
            n += 1
            if n > limit { throw CancellationError() }
        }
    }
}

@MainActor
@Test func otaFollowsProgressOnlyWhileTheKnobIsBusy() async throws {
    let server = OTAServer()
    let m = otaModel(server)
    await m.load()
    let idleTicks = Ticks(2)
    var reloads = 0
    await m.ota.followProgress(sleep: { _ in try idleTicks.tick() }, finished: { reloads += 1 })
    #expect(server.log.filter { $0 == "GET /v1/devices/knob-61fc8c/ota" }.count == 1)
    #expect(reloads == 0)
    _ = await m.ota.update(to: "0.9.14")
    #expect(m.ota.status?.phase == .downloading)
    let ticks = Ticks(1)
    await m.ota.followProgress(sleep: { _ in try ticks.tick() }, finished: { reloads += 1 })
    #expect(server.log.filter { $0 == "GET /v1/devices/knob-61fc8c/ota" }.count == 2)
    #expect(m.ota.status?.phase == .idle)
    #expect(reloads == 1)
}
