import Foundation

public enum CinderLineCodec {
    public static let prefix = "CINDER1 "
    public static let maxLineBytes = 1024
    public static let maxURLLength = 128
    public static let maxTokenLength = 64
    public static let maxNameBytes = 32

    public enum ResetScope: String, Sendable, Codable { case factory, ember, wifi }

    public enum Request: Equatable, Sendable {
        case info
        case status
        case setEmber(url: String, deviceID: String, token: String, name: String)
        case reset(ResetScope)
        case reboot
    }

    public enum EncodeError: Error, Equatable {
        case badURL, tooLong
    }

    public static func encode(_ request: Request, id: Int) throws -> [UInt8] {
        var obj: [String: JSONValue] = ["id": .int(id)]
        switch request {
        case .info: obj["op"] = .string("info")
        case .status: obj["op"] = .string("status")
        case .reboot: obj["op"] = .string("reboot")
        case .reset(let scope):
            obj["op"] = .string("reset")
            obj["scope"] = .string(scope.rawValue)
        case .setEmber(let raw, let deviceID, let token, let name):
            guard let url = normalizedEmberURL(raw) else { throw EncodeError.badURL }
            guard token.utf8.count <= maxTokenLength, name.utf8.count <= maxNameBytes else { throw EncodeError.tooLong }
            obj["op"] = .string("set_ember")
            obj["url"] = .string(url)
            obj["device_id"] = .string(deviceID)
            obj["token"] = .string(token)
            obj["name"] = .string(name)
        }
        let enc = JSONEncoder()
        enc.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        let line = Array(prefix.utf8) + Array(try enc.encode(JSONValue.object(obj))) + [0x0A]
        guard line.count <= maxLineBytes else { throw EncodeError.tooLong }
        return line
    }

    public static func isValidEmberURL(_ s: String) -> Bool { normalizedEmberURL(s) != nil }

    public static func normalizedEmberURL(_ raw: String) -> String? {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard s.count <= maxURLLength, let sep = s.range(of: "://"),
              s[..<sep.lowerBound].lowercased() == "http" else { return nil }
        var rest = Substring(s[sep.upperBound...])
        if rest.hasSuffix("/") { rest = rest.dropLast() }
        let parts = rest.split(separator: ":", maxSplits: 2, omittingEmptySubsequences: false)
        guard parts.count <= 2 else { return nil }
        let host = parts[0].lowercased()
        guard !host.isEmpty, host.unicodeScalars.allSatisfy({ ("a"..."z").contains($0) || ("0"..."9").contains($0) || $0 == "." || $0 == "-" }),
              !host.hasPrefix("."), !host.hasPrefix("-"), !host.contains("..") else { return nil }
        if host.allSatisfy({ $0.isNumber || $0 == "." }) {
            let octets = host.split(separator: ".", omittingEmptySubsequences: false)
            guard octets.count == 4, octets.allSatisfy({ !$0.isEmpty && $0.count <= 3 && Int($0).map { $0 <= 255 } == true })
            else { return nil }
        }
        var out = "http://\(host)"
        if parts.count == 2 {
            guard let port = Int(parts[1]), parts[1].allSatisfy(\.isNumber), (1...65535).contains(port) else { return nil }
            out += ":\(port)"
        }
        return out
    }

    /// Cut to the knob's 32 UTF-8 bytes, on a character boundary.
    public static func cappedName(_ name: String) -> String {
        var n = name
        while n.utf8.count > maxNameBytes { n.removeLast() }
        return n
    }

    public static func decode(line: String) -> Message? {
        var l = line
        if l.hasSuffix("\r") { l.removeLast() }
        guard l.hasPrefix(prefix) else { return nil }
        let json = Data(l.dropFirst(prefix.count).utf8)
        let dec = JSONDecoder()
        if let ev = try? dec.decode(Event.self, from: json), ev.ev != nil { return .event(ev) }
        if let reply = try? dec.decode(Reply.self, from: json), reply.id != nil { return .reply(reply) }
        return nil
    }

    public enum Message: Equatable, Sendable {
        case reply(Reply)
        case event(Event)
    }

    public struct Wifi: Codable, Equatable, Sendable {
        public var configured: Bool?
        public var state: String?
        public var ssid: String?
        public var ip: String?
        public var rssi: Int?
        public init(configured: Bool? = nil, state: String? = nil, ssid: String? = nil, ip: String? = nil, rssi: Int? = nil) {
            self.configured = configured; self.state = state; self.ssid = ssid; self.ip = ip; self.rssi = rssi
        }
    }

    public struct Ember: Codable, Equatable, Sendable {
        public var configured: Bool?
        public var state: String?
        public var lastCheckinS: Int?
        public var configVersion: Int?
        enum CodingKeys: String, CodingKey {
            case configured, state
            case lastCheckinS = "last_checkin_s"
            case configVersion = "config_version"
        }
        public init(configured: Bool? = nil, state: String? = nil, lastCheckinS: Int? = nil, configVersion: Int? = nil) {
            self.configured = configured; self.state = state; self.lastCheckinS = lastCheckinS; self.configVersion = configVersion
        }
    }

    public struct Heap: Codable, Equatable, Sendable {
        public var internalFree: Int?
        public var internalLargest: Int?
        public var psramFree: Int?
        enum CodingKeys: String, CodingKey {
            case internalFree = "internal_free"
            case internalLargest = "internal_largest"
            case psramFree = "psram_free"
        }
    }

    public struct Reply: Codable, Equatable, Sendable {
        public var id: Int?
        public var ok: Bool
        public var error: String?
        public var fw: String?
        public var hwID: String?
        public var deviceID: String?
        public var wifi: Wifi?
        public var ember: Ember?
        public var heap: Heap?
        enum CodingKeys: String, CodingKey {
            case id, ok, error, fw, wifi, ember, heap
            case hwID = "hw_id"
            case deviceID = "device_id"
        }
        public init(id: Int?, ok: Bool, error: String? = nil, fw: String? = nil, hwID: String? = nil,
                    deviceID: String? = nil, wifi: Wifi? = nil, ember: Ember? = nil, heap: Heap? = nil) {
            self.id = id; self.ok = ok; self.error = error; self.fw = fw; self.hwID = hwID
            self.deviceID = deviceID; self.wifi = wifi; self.ember = ember; self.heap = heap
        }
    }

    public struct Event: Codable, Equatable, Sendable {
        public var ev: String?
        public var state: String?
        public var fw: String?
        public var provisioned: Bool?
        public init(ev: String, state: String? = nil, fw: String? = nil, provisioned: Bool? = nil) {
            self.ev = ev; self.state = state; self.fw = fw; self.provisioned = provisioned
        }
    }

    public static func line<T: Encodable>(_ value: T) -> [UInt8] {
        let enc = JSONEncoder()
        enc.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return Array(prefix.utf8) + Array((try? enc.encode(value)) ?? Data("{}".utf8)) + [0x0A]
    }
}
