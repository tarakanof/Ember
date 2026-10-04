import Testing
import Foundation
@testable import EmberKit

/// The shared vectors in testdata/knob (cinder's firmware tests read the
/// same files).
enum KnobVectors {
    static var dir: URL {
        URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("testdata/knob")
    }

    static func load(_ name: String) throws -> [String: Any] {
        let data = try Data(contentsOf: dir.appendingPathComponent(name))
        return try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    static func bytes(hex: String) -> [UInt8] {
        var out: [UInt8] = []
        var i = hex.startIndex
        while i < hex.endIndex {
            let j = hex.index(i, offsetBy: 2)
            out.append(UInt8(hex[i..<j], radix: 16)!)
            i = j
        }
        return out
    }
}

private func expected(_ m: [String: Any]) -> ImprovCodec.Message? {
    switch m["type"] as? String {
    case "rpc": return .rpc(command: UInt8(m["command"] as! Int), strings: m["strings"] as! [String])
    case "result": return .result(command: UInt8(m["command"] as! Int), strings: m["strings"] as! [String])
    case "state": return .state(ImprovCodec.State(rawValue: UInt8(m["state"] as! Int))!)
    case "error": return .error(ImprovCodec.ErrorCode(rawValue: UInt8(m["error"] as! Int))!)
    default: return nil
    }
}

@Test func improvVectorsDecode() throws {
    let v = try KnobVectors.load("improv.json")
    let frames = try #require(v["frames"] as? [[String: Any]])
    #expect(frames.count >= 10)
    for f in frames {
        let bytes = KnobVectors.bytes(hex: f["hex"] as! String)
        let want = try #require(expected(f["message"] as! [String: Any]))
        #expect(try ImprovCodec.decode(bytes) == want, "\(f["name"]!)")
        #expect(try ImprovCodec.decode(bytes + [0x0A]) == want, "\(f["name"]!) with newline")
    }
}

@Test func improvVectorsEncodeHostFrames() throws {
    let v = try KnobVectors.load("improv.json")
    for f in (v["frames"] as! [[String: Any]]) where f["direction"] as? String == "host" {
        let m = f["message"] as! [String: Any]
        let cmd = try #require(ImprovCodec.Command(rawValue: UInt8(m["command"] as! Int)))
        let bytes = try ImprovCodec.rpc(cmd, m["strings"] as! [String])
        #expect(bytes == KnobVectors.bytes(hex: f["hex"] as! String) + [0x0A], "\(f["name"]!)")
    }
}

@Test func improvVectorsEncodeDeviceFrames() throws {
    let v = try KnobVectors.load("improv.json")
    for f in (v["frames"] as! [[String: Any]]) where f["direction"] as? String == "device" {
        let want = KnobVectors.bytes(hex: f["hex"] as! String) + [0x0A]
        switch try #require(expected(f["message"] as! [String: Any])) {
        case .state(let s): #expect(ImprovCodec.state(s) == want)
        case .error(let e): #expect(ImprovCodec.error(e) == want)
        case .result(let c, let s): #expect(try ImprovCodec.result(ImprovCodec.Command(rawValue: c)!, s) == want)
        default: Issue.record("unexpected device frame \(f["name"]!)")
        }
    }
}

@Test func improvVectorsRejectInvalid() throws {
    let v = try KnobVectors.load("improv.json")
    let invalid = try #require(v["invalid"] as? [[String: Any]])
    for f in invalid {
        let bytes = KnobVectors.bytes(hex: f["hex"] as! String)
        let name = f["error"] as! String
        #expect(throws: ImprovCodec.DecodeError.self, "\(f["name"]!)") { try ImprovCodec.decode(bytes) }
        do {
            _ = try ImprovCodec.decode(bytes)
        } catch let e as ImprovCodec.DecodeError {
            #expect("\(e)" == name, "\(f["name"]!)")
        }
    }
}

@Test func improvChecksumOfNewlineIsNotStripped() throws {
    // A frame whose checksum byte is 0x0a still decodes without a newline.
    for b in UInt8(0)...UInt8(255) {
        let f = Array(ImprovCodec.frame(0x01, [b]).dropLast())
        if f.last == 0x0A {
            #expect(throws: Never.self) { _ = try ImprovCodec.decode(f) }
            return
        }
    }
}

@Test func improvRejectsOversizedStrings() {
    #expect(throws: ImprovCodec.DecodeError.self) {
        try ImprovCodec.wifiSettings(ssid: String(repeating: "a", count: 200), password: String(repeating: "b", count: 100))
    }
}

@Test func deviceInfoAndScanParsing() {
    let info = ImprovDeviceInfo(strings: ["cinder", "0.5.0", "ESP32-S3", "Knob 61FC8C"])
    #expect(info?.isCinder == true)
    #expect(ImprovDeviceInfo(strings: ["esphome", "1", "ESP32"]) == nil)
    #expect(KnobWiFiNetwork(strings: []) == nil)
    let list = [KnobWiFiNetwork(ssid: "b", rssi: -70, secured: true), KnobWiFiNetwork(ssid: "a", rssi: -50, secured: false),
                KnobWiFiNetwork(ssid: "b", rssi: -40, secured: true), KnobWiFiNetwork(ssid: "c", rssi: -50, secured: true)]
    #expect(KnobWiFiNetwork.dedupe(list).map(\.ssid) == ["b", "a", "c"])
    #expect(KnobWiFiNetwork.dedupe(list).first?.rssi == -40)
}

// MARK: CINDER1

private func request(_ r: [String: Any], token: String? = nil) -> CinderLineCodec.Request? {
    switch r["op"] as? String {
    case "info": return .info
    case "status": return .status
    case "reboot": return .reboot
    case "reset": return .reset(CinderLineCodec.ResetScope(rawValue: r["scope"] as! String)!)
    case "set_ember":
        return .setEmber(url: r["url"] as! String, deviceID: r["device_id"] as! String,
                         token: r["token"] as! String, name: r["name"] as! String)
    default: return nil
    }
}

@Test func cinderVectorsEncodeHostLines() throws {
    let v = try KnobVectors.load("cinder1.json")
    for h in (v["host"] as! [[String: Any]]) {
        let req = try #require(request(h["request"] as! [String: Any]))
        let line = try CinderLineCodec.encode(req, id: h["id"] as! Int)
        #expect(String(decoding: line, as: UTF8.self) == h["line"] as! String, "\(h["name"]!)")
    }
}

@Test func cinderVectorsRejectBadHostInput() throws {
    let v = try KnobVectors.load("cinder1.json")
    for h in (v["host_rejected"] as! [[String: Any]]) {
        let req = CinderLineCodec.Request.setEmber(url: h["url"] as! String, deviceID: "knob-61fc8c",
                                                   token: h["token"] as? String ?? "ekd_x",
                                                   name: h["name"] as? String ?? "Desk knob")
        let want: CinderLineCodec.EncodeError = h["error"] as! String == "bad_url" ? .badURL : .tooLong
        #expect(throws: want, "\(h["name"]!)") { try CinderLineCodec.encode(req, id: 1) }
    }
}

@Test func cinderVectorsDecodeDeviceLines() throws {
    let v = try KnobVectors.load("cinder1.json")
    for d in (v["device"] as! [[String: Any]]) {
        let name = d["name"] as! String
        let msg = CinderLineCodec.decode(line: d["line"] as! String)
        switch d["kind"] as! String {
        case "reply":
            guard case .reply(let r)? = msg else { Issue.record("\(name): \(String(describing: msg))"); continue }
            #expect(r.id == d["id"] as? Int, "\(name)")
            #expect(r.ok == d["ok"] as! Bool, "\(name)")
            if let e = d["error"] as? String { #expect(r.error == e) }
            if let hw = d["hw_id"] as? String { #expect(r.hwID == hw) }
            if let s = d["ember_state"] as? String { #expect(r.ember?.state == s) }
            if let ip = d["wifi_ip"] as? String { #expect(r.wifi?.ip == ip) }
        case "event":
            guard case .event(let e)? = msg else { Issue.record("\(name): \(String(describing: msg))"); continue }
            #expect(e.ev == d["ev"] as? String, "\(name)")
            if let s = d["state"] as? String { #expect(e.state == s) }
            if let p = d["provisioned"] as? Bool { #expect(e.provisioned == p) }
        default:
            #expect(msg == nil, "\(name)")
        }
    }
}

@Test func emberURLValidation() {
    #expect(CinderLineCodec.isValidEmberURL("http://192.168.0.2:3627"))
    #expect(CinderLineCodec.isValidEmberURL("http://ember.lan/"))
    #expect(!CinderLineCodec.isValidEmberURL("https://ember.lan"))
    #expect(!CinderLineCodec.isValidEmberURL("http://192.168.0.2:3627/api"))
    #expect(!CinderLineCodec.isValidEmberURL("192.168.0.2"))
    #expect(!CinderLineCodec.isValidEmberURL("http://u:p@host"))
    #expect(!CinderLineCodec.isValidEmberURL("http://192.168.0.2:0"))
}

@Test func cinderVectorsNormalizeURLs() throws {
    let v = try KnobVectors.load("cinder1.json")
    for n in (v["url_normalized"] as! [[String: String]]) {
        #expect(CinderLineCodec.normalizedEmberURL(n["in"]!) == n["out"]!, "\(n["in"]!)")
    }
    for h in (v["host_rejected"] as! [[String: Any]]) where h["error"] as? String == "bad_url" {
        #expect(CinderLineCodec.normalizedEmberURL(h["url"] as! String) == nil, "\(h["name"]!)")
    }
}

@Test func knobNameIsCappedAt32Bytes() {
    #expect(CinderLineCodec.cappedName("Desk knob") == "Desk knob")
    let long = String(repeating: "é", count: 20) // 40 bytes
    let capped = CinderLineCodec.cappedName(long)
    #expect(capped.utf8.count <= 32)
    #expect(capped == String(repeating: "é", count: 16))
}

// MARK: Demuxer

@Test func demuxerSplitsInterleavedStream() throws {
    var d = KnobStreamDemuxer()
    var stream: [UInt8] = Array("I (100) boot: hello\r\n".utf8)
    stream += try ImprovCodec.result(.deviceInfo, ["cinder", "0.5.0", "ESP32-S3", "Knob\n61FC8C"])
    stream += Array("partial log ".utf8)
    stream += ImprovCodec.state(.provisioned)
    stream += Array("CINDER1 {\"ev\":\"ember\",\"state\":\"ok\"}\nW (5) wifi: IMPROV in a log line\n".utf8)
    var events: [KnobEvent] = []
    // Byte by byte: frames and lines must survive any split.
    for b in stream { events += d.feed([b]) }
    #expect(events == [
        .log("I (100) boot: hello"),
        .improv(.result(command: 3, strings: ["cinder", "0.5.0", "ESP32-S3", "Knob\n61FC8C"])),
        .log("partial log "),
        .improv(.state(.provisioned)),
        .cinder(.event(.init(ev: "ember", state: "ok"))),
        .log("W (5) wifi: IMPROV in a log line"),
    ])
}

@Test func demuxerSkipsCorruptFrames() {
    var d = KnobStreamDemuxer()
    var bad = ImprovCodec.state(.ready)
    bad[bad.count - 2] &+= 1
    let events = d.feed(bad + ImprovCodec.state(.provisioned))
    #expect(events.contains(.improv(.state(.provisioned))))
    #expect(!events.contains(.improv(.state(.ready))))
}

@Test func demuxerBoundsItsBuffer() {
    var d = KnobStreamDemuxer()
    _ = d.feed([UInt8](repeating: 0x41, count: 10_000))
    let events = d.feed(Array("\nCINDER1 {\"id\":1,\"ok\":true}\n".utf8))
    #expect(events.last == .cinder(.reply(.init(id: 1, ok: true))))
}
