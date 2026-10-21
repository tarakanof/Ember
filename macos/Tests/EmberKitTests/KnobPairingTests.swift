import Testing
import Foundation
@testable import EmberKit

private func deviceJSON(id: String, hw: String, name: String) -> String {
    #"{"id":"\#(id)","kind":"cinder-knob","hw_id":"\#(hw)","name":"\#(name)","created_at":"2026-10-20T10:00:00Z","config_version":1,"rotation_pending":false,"rotated_at":null}"#
}

private func claimJSON(id: String, hw: String, known: String?) -> String {
    #"{"id":"\#(id)","hw_id":"\#(hw)","short_id":"\#(hw.suffix(6).uppercased())","fw":"0.9.110","ip":"192.168.1.40","created_at":"2026-10-20T10:00:00Z","expires_at":"2026-10-20T10:10:00Z","known_device_id":\#(known.map { "\"\($0)\"" } ?? "null")}"#
}

private final class FakePairingServer: @unchecked Sendable {
    private let lock = NSLock()
    private var _claims: [String]
    private var _devices: [String]
    private var _requests: [(method: String, path: String, body: String)] = []
    private var _claimsStatus = 200
    private var _attemptsLeft = 4
    private var _code = "482913"
    private var _approveStatus: Int?
    private let listGate = DispatchSemaphore(value: 0)
    private var _holdList = false
    private var _features = #"["lan_pair"]"#
    private var _declineStatus: Int?

    init(claims: [String], devices: [String] = []) { _claims = claims; _devices = devices }

    var requests: [(method: String, path: String, body: String)] { lock.withLock { _requests } }
    func setClaimsStatus(_ s: Int) { lock.withLock { _claimsStatus = s } }
    func setApproveStatus(_ s: Int) { lock.withLock { _approveStatus = s } }
    func setDeclineStatus(_ s: Int) { lock.withLock { _declineStatus = s } }
    func setFeatures(_ f: String) { lock.withLock { _features = f } }
    func holdNextList() { lock.withLock { _holdList = true } }
    func releaseList() { listGate.signal() }
    func setWrongCodeLeft(_ n: Int) { lock.withLock { _attemptsLeft = n } }
    func setClaims(_ c: [String]) { lock.withLock { _claims = c } }

    func handle(_ req: URLRequest) -> (HTTPURLResponse, Data) {
        let body = String(decoding: req.httpBody ?? req.httpBodyStreamData() ?? Data(), as: UTF8.self)
        let method = req.httpMethod ?? "GET"
        let path = req.url!.path
        if method == "GET", path == "/v1/devices/claims" {
            let hold = lock.withLock { let h = _holdList; _holdList = false; return h }
            let snapshot = lock.withLock { _claims }
            lock.withLock { _requests.append((method, path, body)) }
            if hold {
                listGate.wait()
                return (okResponse(req.url!), Data(#"{"claims":[\#(snapshot.joined(separator: ","))]}"#.utf8))
            }
        }
        return lock.withLock {
            if !(method == "GET" && path == "/v1/devices/claims") { _requests.append((method, path, body)) }
            switch (method, path) {
            case ("GET", "/v1/devices/claims"):
                if _claimsStatus != 200 { return (okResponse(req.url!, status: _claimsStatus), Data(#"{"error":"not found"}"#.utf8)) }
                return (okResponse(req.url!), Data(#"{"claims":[\#(_claims.joined(separator: ","))]}"#.utf8))
            case ("GET", "/version"):
                return (okResponse(req.url!), Data(#"{"binary":"ember","version":"0.71.0","features":\#(_features)}"#.utf8))
            case ("GET", "/v1/devices"):
                return (okResponse(req.url!), Data(#"{"devices":[\#(_devices.joined(separator: ","))]}"#.utf8))
            case ("POST", let p) where p.hasPrefix("/v1/devices/claims/") && p.hasSuffix("/approve"):
                if let s = _approveStatus { return (okResponse(req.url!, status: s), Data(#"{"error":"not found"}"#.utf8)) }
                let obj = try? JSONSerialization.jsonObject(with: Data(body.utf8)) as? [String: String]
                guard obj?["code"] == _code else {
                    let left = _attemptsLeft
                    _attemptsLeft -= 1
                    return (okResponse(req.url!, status: 400), Data(#"{"error":"wrong_code","attempts_left":\#(left)}"#.utf8))
                }
                _claims = []
                _devices = [deviceJSON(id: "knob-61fc8c", hw: "3cdc7561fc8c", name: "Knob 61FC8C")]
                return (okResponse(req.url!), Data(_devices[0].utf8))
            case ("POST", let p) where p.hasPrefix("/v1/devices/claims/") && p.hasSuffix("/decline"):
                if let s = _declineStatus { return (okResponse(req.url!, status: s), Data(#"{"error":"already_approved"}"#.utf8)) }
                _claims = []
                return (okResponse(req.url!, status: 204), Data())
            case ("DELETE", let p) where p.hasPrefix("/v1/devices/knob-"):
                _devices.removeAll { $0.contains("\"\(p.dropFirst("/v1/devices/".count))\"") }
                return (okResponse(req.url!, status: 204), Data())
            default:
                return (okResponse(req.url!, status: 404), Data(#"{"error":"not found"}"#.utf8))
            }
        }
    }
}

@MainActor
private func pairingModel(_ server: FakePairingServer) -> KnobModel {
    let svc = KnobService(client: stubbedClient(token: "t") { server.handle($0) })
    return KnobModel(service: svc, ports: KnobSerialPorts(scanner: { [] }),
                     opener: FakeOpener(FakeKnob()), debounce: .zero, sleep: { _ in })
}

@Test func claimListDecodesTheServerShape() async throws {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: "knob-61fc8c"),
                                            claimJSON(id: "c_deadbeef", hw: "3cdc7500aaaa", known: nil)])
    let svc = KnobService(client: stubbedClient(token: "t") { server.handle($0) })
    let claims = try await svc.claims()
    #expect(claims.map(\.id) == ["c_0a1b2c3d", "c_deadbeef"])
    #expect(claims[0].shortID == "61FC8C" && claims[0].fw == "0.9.110" && claims[0].ip == "192.168.1.40")
    #expect(claims[0].knownDeviceID == "knob-61fc8c" && claims[1].knownDeviceID == nil)
}

@MainActor
@Test func pairingRefreshListsPendingClaims() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    #expect(m.pairing.claims.map(\.id) == ["c_0a1b2c3d"] && m.pairing.isAvailable)
}

@MainActor
@Test func pairingIsHiddenOnAServerWithoutClaims() async {
    let server = FakePairingServer(claims: [])
    server.setClaimsStatus(404)
    let m = pairingModel(server)
    await m.pairing.refresh()
    #expect(!m.pairing.isAvailable && m.pairing.claims.isEmpty)
    await m.pairing.poll()
    #expect(server.requests.filter { $0.path == "/v1/devices/claims" }.count == 2)
}

@Test func pairingCodeAcceptsSpacesAndOnlySixDigits() {
    #expect(KnobPairingCode.normalized("482 913") == "482913")
    #expect(KnobPairingCode.isValid("482 913") && KnobPairingCode.isValid("482913"))
    #expect(!KnobPairingCode.isValid("48291") && !KnobPairingCode.isValid("4829133") && !KnobPairingCode.isValid("48a913"))
}

@MainActor
@Test func approveSendsTheCodeWithoutSpacesAndReloadsTheRegistry() async throws {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    let claim = try #require(m.pairing.claims.first)
    let ok = await m.approve(claim, code: "482 913")
    #expect(ok)
    let post = try #require(server.requests.first { $0.path == "/v1/devices/claims/c_0a1b2c3d/approve" })
    let obj = try #require(try JSONSerialization.jsonObject(with: Data(post.body.utf8)) as? [String: String])
    #expect(obj == ["code": "482913"])
    #expect(m.pairing.claims.isEmpty)
    #expect(m.knob?.id == "knob-61fc8c")
}

@MainActor
@Test func approveWithAnInvalidCodeNeverCallsTheServer() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    let ok = await m.approve(m.pairing.claims[0], code: "12")
    #expect(!ok)
    #expect(!server.requests.contains { $0.path.hasSuffix("/approve") })
}

@MainActor
@Test func wrongCodeReportsAttemptsLeftAndKeepsTheClaim() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    let claim = m.pairing.claims[0]
    #expect(await m.approve(claim, code: "000000") == false)
    #expect(m.pairing.failures[claim.id] == .wrongCode(attemptsLeft: 4))
    #expect(m.pairing.claims.map(\.id) == [claim.id])
    #expect(await m.approve(claim, code: "482913"))
    #expect(m.pairing.failures[claim.id] == nil)
}

@MainActor
@Test func approvingAnExpiredClaimReportsItGoneAndRefreshes() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    let claim = m.pairing.claims[0]
    server.setApproveStatus(404)
    server.setClaims([])
    #expect(await m.approve(claim, code: "482913") == false)
    #expect(m.pairing.notices.map(\.closure) == [.gone] && m.pairing.notices.map(\.knob) == ["61FC8C"])
    #expect(m.pairing.claims.isEmpty && m.pairing.isVisible)
}

@MainActor
@Test func declineRemovesTheClaimThroughThePostRoute() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    await m.pairing.decline(m.pairing.claims[0])
    #expect(server.requests.contains { $0.method == "POST" && $0.path == "/v1/devices/claims/c_0a1b2c3d/decline" })
    #expect(!server.requests.contains { $0.method == "DELETE" })
    #expect(m.pairing.claims.isEmpty)
}

@MainActor
@Test func aDifferentRegisteredKnobIsReplacedAfterApproval() async throws {
    let old = deviceJSON(id: "knob-aaaaaa", hw: "3cdc7500aaaa", name: "Old knob")
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)], devices: [old])
    let m = pairingModel(server)
    await m.load()
    #expect(m.knob?.id == "knob-aaaaaa")
    await m.pairing.refresh()
    let claim = m.pairing.claims[0]
    #expect(m.pairing.replaces(claim, current: m.knob)?.id == "knob-aaaaaa")
    #expect(await m.approve(claim, code: "482913"))
    let approveAt = try #require(server.requests.firstIndex { $0.path.hasSuffix("/approve") })
    let deleteAt = try #require(server.requests.firstIndex { $0.method == "DELETE" && $0.path == "/v1/devices/knob-aaaaaa" })
    #expect(deleteAt > approveAt)
}

@MainActor
@Test func aRepairOfTheRegisteredKnobReplacesNothing() async {
    let current = KnobDevice(id: "knob-61fc8c", hwID: "3cdc7561fc8c", name: "Desk", createdAt: Date())
    let m = KnobPairingModel(service: KnobService(client: stubbedClient { FakePairingServer(claims: []).handle($0) }))
    let claim = KnobClaim(id: "c_1", hwID: "3cdc7561fc8c", shortID: "61FC8C", createdAt: Date(), expiresAt: Date(), knownDeviceID: "knob-61fc8c")
    #expect(m.replaces(claim, current: current) == nil)
    #expect(m.replaces(claim, current: nil) == nil)
}

@Test func forgetTextGateFollowsTheLanPairCap() {
    var knob = KnobDevice(id: "knob-61fc8c", hwID: "3cdc7561fc8c", name: "Desk", createdAt: Date())
    #expect(!knob.supports(feature: "lan_pair"))
    knob.effectiveCaps = KnobCaps(view: [1], pages: [], features: ["lan_pair"])
    #expect(knob.supports(feature: "lan_pair"))
}

@MainActor
@Test func decliningAnApprovedClaimShowsAlreadyApproved() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    let claim = m.pairing.claims[0]
    server.setDeclineStatus(409)
    server.setClaims([])
    await m.pairing.decline(claim)
    #expect(m.pairing.notices.map(\.closure) == [.alreadyApproved] && m.pairing.isVisible)
}

@MainActor
@Test func pairingIsHiddenOnA405Too() async {
    let server = FakePairingServer(claims: [])
    server.setClaimsStatus(405)
    let m = pairingModel(server)
    await m.pairing.refresh()
    #expect(!m.pairing.isAvailable)
}

@MainActor
@Test func pairingIsHiddenWhenVersionLacksLanPairAndNeverAsksForClaims() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    server.setFeatures(#"["firmware_delete_keep"]"#)
    let m = pairingModel(server)
    await m.pairing.refresh()
    #expect(!m.pairing.isAvailable && m.pairing.claims.isEmpty)
    #expect(!server.requests.contains { $0.path == "/v1/devices/claims" })
}

@MainActor
@Test func approvingAClaimOfAKnobThatAlreadyPairedDropsItWithAMessage() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    let claim = m.pairing.claims[0]
    server.setApproveStatus(409)
    server.setClaims([])
    #expect(await m.approve(claim, code: "482913") == false)
    #expect(m.pairing.notices.map(\.closure) == [.alreadyPaired])
    #expect(m.pairing.claims.isEmpty && m.pairing.isVisible)
    await m.pairing.refresh()
    #expect(m.pairing.notices.map(\.closure) == [.alreadyPaired])
}

@MainActor
@Test func fifthWrongCodeClosesTheClaimWithANoticeThatSurvivesRefresh() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    server.setWrongCodeLeft(0)
    let m = pairingModel(server)
    await m.pairing.refresh()
    let claim = m.pairing.claims[0]
    server.setClaims([])
    #expect(await m.approve(claim, code: "000000") == false)
    await m.pairing.refresh()
    #expect(m.pairing.notices.map(\.closure) == [.wrongCodes] && m.pairing.isVisible)
}

@MainActor
@Test func aNoticeIsDismissedByTheUser() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    server.setApproveStatus(404)
    server.setClaims([])
    _ = await m.approve(m.pairing.claims[0], code: "482913")
    m.pairing.dismiss(m.pairing.notices[0])
    #expect(m.pairing.notices.isEmpty && !m.pairing.isVisible)
}

@MainActor
@Test func aNoticeOnlyAgesWhileThePaneIsPolling() async throws {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let svc = KnobService(client: stubbedClient(token: "t") { server.handle($0) })
    let p = KnobPairingModel(service: svc, interval: .seconds(3), noticeTTL: .seconds(10), sleep: { _ in
        try await Task.sleep(for: .milliseconds(1))
    })
    await p.refresh()
    server.setApproveStatus(404)
    _ = await p.approve(p.claims[0], code: "482913")
    try await Task.sleep(for: .milliseconds(100))
    #expect(p.notices.count == 1)
    let polling = Task { await p.poll() }
    for _ in 0..<200 where !p.notices.isEmpty { try await Task.sleep(for: .milliseconds(10)) }
    polling.cancel()
    #expect(p.notices.isEmpty)
}

@MainActor
@Test func pollingReadsVersionAgainSoAServerUpgradeShowsPairing() async {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    server.setFeatures("[]")
    let m = pairingModel(server)
    await m.pairing.refresh()
    #expect(!m.pairing.isAvailable)
    server.setFeatures(#"["lan_pair"]"#)
    let task = Task { await m.pairing.poll() }
    for _ in 0..<100 where m.pairing.claims.isEmpty { try? await Task.sleep(for: .milliseconds(10)) }
    task.cancel()
    #expect(m.pairing.isAvailable && m.pairing.claims.map(\.id) == ["c_0a1b2c3d"])
}

@MainActor
@Test func aListReadStartedBeforeApprovalDoesNotBringTheClaimBack() async throws {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    let claim = m.pairing.claims[0]
    server.holdNextList()
    let stale = Task { await m.pairing.refresh() }
    try await Task.sleep(for: .milliseconds(50))
    #expect(await m.approve(claim, code: "482913"))
    server.releaseList()
    await stale.value
    #expect(m.pairing.claims.isEmpty)
}

@MainActor
@Test func refreshesRunOneAtATime() async throws {
    let server = FakePairingServer(claims: [claimJSON(id: "c_0a1b2c3d", hw: "3cdc7561fc8c", known: nil)])
    let m = pairingModel(server)
    await m.pairing.refresh()
    let before = server.requests.filter { $0.path == "/v1/devices/claims" }.count
    server.holdNextList()
    let a = Task { await m.pairing.refresh() }
    try await Task.sleep(for: .milliseconds(50))
    let b = Task { await m.pairing.refresh() }
    try await Task.sleep(for: .milliseconds(50))
    #expect(server.requests.filter { $0.path == "/v1/devices/claims" }.count == before + 1)
    server.releaseList()
    await a.value; await b.value
    #expect(server.requests.filter { $0.path == "/v1/devices/claims" }.count == before + 2)
}
