import Foundation

public enum ImprovCodec {
    public static let header: [UInt8] = Array("IMPROV".utf8)
    public static let version: UInt8 = 1
    static let prefixLength = header.count + 3

    public enum PacketType: UInt8, Sendable {
        case currentState = 0x01
        case errorState = 0x02
        case rpc = 0x03
        case rpcResult = 0x04
    }

    public enum Command: UInt8, Sendable {
        case wifiSettings = 0x01
        case currentState = 0x02
        case deviceInfo = 0x03
        case scanNetworks = 0x04
        case hostname = 0x05
        case deviceName = 0x06
    }

    public enum State: UInt8, Sendable {
        case stopped = 0x00
        case authorizationRequired = 0x01
        case ready = 0x02
        case provisioning = 0x03
        case provisioned = 0x04
    }

    public enum ErrorCode: UInt8, Sendable {
        case none = 0x00
        case invalidRPC = 0x01
        case unknownCommand = 0x02
        case unableToConnect = 0x03
        case notAuthorized = 0x04
        case badHostname = 0x05
        case unknown = 0xFF
    }

    public enum Message: Equatable, Sendable {
        case state(State)
        case error(ErrorCode)
        case result(command: UInt8, strings: [String])
        case rpc(command: UInt8, strings: [String])
        case other(type: UInt8, data: [UInt8])
    }

    public enum DecodeError: Error, Equatable {
        case tooShort, badHeader, badVersion, badLength, badChecksum, badStrings
    }

    public static func frame(_ type: UInt8, _ data: [UInt8]) -> [UInt8] {
        precondition(data.count <= 255, "Improv data is at most 255 bytes")
        var out = header + [version, type, UInt8(data.count)] + data
        out.append(checksum(out))
        out.append(0x0A)
        return out
    }

    public static func checksum<C: Collection>(_ bytes: C) -> UInt8 where C.Element == UInt8 {
        UInt8(truncatingIfNeeded: bytes.reduce(0) { $0 &+ Int($1) })
    }

    public static func rpc(_ command: Command, _ strings: [String] = []) throws -> [UInt8] {
        let payload = try lengthPrefixed(strings)
        return frame(PacketType.rpc.rawValue, [command.rawValue, UInt8(payload.count)] + payload)
    }

    public static func wifiSettings(ssid: String, password: String) throws -> [UInt8] {
        try rpc(.wifiSettings, [ssid, password])
    }

    public static func result(_ command: Command, _ strings: [String]) throws -> [UInt8] {
        let payload = try lengthPrefixed(strings)
        return frame(PacketType.rpcResult.rawValue, [command.rawValue, UInt8(payload.count)] + payload)
    }

    public static func state(_ s: State) -> [UInt8] { frame(PacketType.currentState.rawValue, [s.rawValue]) }
    public static func error(_ e: ErrorCode) -> [UInt8] { frame(PacketType.errorState.rawValue, [e.rawValue]) }

    static func lengthPrefixed(_ strings: [String]) throws -> [UInt8] {
        var out: [UInt8] = []
        for s in strings {
            let b = Array(s.utf8)
            guard b.count <= 255 else { throw DecodeError.badStrings }
            out.append(UInt8(b.count))
            out += b
        }
        guard out.count <= 253 else { throw DecodeError.badLength }
        return out
    }

    public static func decode(_ bytes: [UInt8]) throws -> Message {
        var b = bytes
        guard b.count >= prefixLength + 1 else { throw DecodeError.tooShort }
        guard Array(b[0..<header.count]) == header else { throw DecodeError.badHeader }
        guard b[header.count] == version else { throw DecodeError.badVersion }
        let len = Int(b[header.count + 2])
        if b.count == prefixLength + len + 2, b.last == 0x0A { b.removeLast() }
        guard b.count == prefixLength + len + 1 else { throw DecodeError.badLength }
        guard checksum(b.dropLast()) == b.last else { throw DecodeError.badChecksum }
        return try message(type: b[header.count + 1], data: Array(b[prefixLength..<(prefixLength + len)]))
    }

    static func message(type: UInt8, data: [UInt8]) throws -> Message {
        switch PacketType(rawValue: type) {
        case .currentState:
            guard data.count == 1, let s = State(rawValue: data[0]) else { return .other(type: type, data: data) }
            return .state(s)
        case .errorState:
            guard data.count == 1 else { return .other(type: type, data: data) }
            return .error(ErrorCode(rawValue: data[0]) ?? .unknown)
        case .rpc, .rpcResult:
            guard data.count >= 2, Int(data[1]) == data.count - 2 else { throw DecodeError.badStrings }
            let strings = try parseStrings(Array(data[2...]))
            return type == PacketType.rpc.rawValue ? .rpc(command: data[0], strings: strings)
                                                    : .result(command: data[0], strings: strings)
        case nil:
            return .other(type: type, data: data)
        }
    }

    static func parseStrings(_ data: [UInt8]) throws -> [String] {
        var out: [String] = []
        var i = 0
        while i < data.count {
            let n = Int(data[i])
            i += 1
            guard i + n <= data.count else { throw DecodeError.badStrings }
            out.append(String(decoding: data[i..<(i + n)], as: UTF8.self))
            i += n
        }
        return out
    }
}

public struct ImprovDeviceInfo: Equatable, Sendable {
    public let firmware: String
    public let version: String
    public let chip: String
    public let name: String

    public init?(strings: [String]) {
        guard strings.count >= 4 else { return nil }
        firmware = strings[0]; version = strings[1]; chip = strings[2]; name = strings[3]
    }

    public init(firmware: String, version: String, chip: String, name: String) {
        self.firmware = firmware; self.version = version; self.chip = chip; self.name = name
    }

    public var isCinder: Bool { firmware.lowercased() == "cinder" }
}

public struct KnobWiFiNetwork: Equatable, Hashable, Sendable, Identifiable {
    public let ssid: String
    public let rssi: Int
    public let secured: Bool
    public var id: String { ssid }

    public init(ssid: String, rssi: Int, secured: Bool) {
        self.ssid = ssid; self.rssi = rssi; self.secured = secured
    }

    public init?(strings: [String]) {
        guard strings.count >= 3, !strings[0].isEmpty else { return nil }
        ssid = strings[0]
        rssi = Int(strings[1]) ?? -100
        secured = strings[2].uppercased() == "YES"
    }

    public static func dedupe(_ list: [KnobWiFiNetwork]) -> [KnobWiFiNetwork] {
        var best: [String: KnobWiFiNetwork] = [:]
        for n in list where best[n.ssid].map({ n.rssi > $0.rssi }) ?? true { best[n.ssid] = n }
        return best.values.sorted { ($0.rssi, $1.ssid) > ($1.rssi, $0.ssid) }
    }
}
