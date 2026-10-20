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

    init(claims: [String], devices: [String] = []) { _claims = claims; _devices = devices }

    var requests: [(method: String, path: String, body: String)] { lock.withLock { _requests } }
    func setClaimsStatus(_ s: Int) { lock.withLock { _claimsStatus = s } }
    func setApproveStatus(_ s: Int) { lock.withLock { _approveStatus = s } }
    func setClaims(_ c: [String]) { lock.withLock { _claims = c } }

    func handle(_ req: URLRequest) -> (HTTPURLResponse, Data) {
        let body = String(decoding: req.httpBody ?? req.httpBodyStreamData() ?? Data(), as: UTF8.self)
        let method = req.httpMethod ?? "GET"
        let path = req.url!.path
        return lock.withLock {
            _requests.append((method, path, body))
            switch (method, path) {
            case ("GET", "/v1/devices/claims"):
                if _claimsStatus != 200 { return (okResponse(req.url!, status: _claimsStatus), Data(#"{"error":"not found"}"#.utf8)) }
                return (okResponse(req.url!), Data(#"{"claims":[\#(_claims.joined(separator: ","))]}"#.utf8))
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
    #expect(server.requests.filter { $0.path == "/v1/devices/claims" }.count == 1)
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
    #expect(m.pairing.failures[claim.id] == .gone)
    #expect(m.pairing.claims.isEmpty)
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
