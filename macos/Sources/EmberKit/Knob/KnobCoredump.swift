import Foundation

public struct KnobCoredump: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var size: Int
    public var fw: String
    public var receivedAt: Date
    public var reason: String
    public var task: String
    public var pc: String

    enum CodingKeys: String, CodingKey {
        case id, size, fw, reason, task, pc
        case receivedAt = "received_at"
    }

    public init(id: String, size: Int, fw: String = "", receivedAt: Date,
                reason: String = "", task: String = "", pc: String = "") {
        self.id = id; self.size = size; self.fw = fw; self.receivedAt = receivedAt
        self.reason = reason; self.task = task; self.pc = pc
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        size = try c.decode(Int.self, forKey: .size)
        receivedAt = try c.decode(Date.self, forKey: .receivedAt)
        fw = try c.decodeIfPresent(String.self, forKey: .fw) ?? ""
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
        task = try c.decodeIfPresent(String.self, forKey: .task) ?? ""
        pc = try c.decodeIfPresent(String.self, forKey: .pc) ?? ""
    }

    public func filename(device: String) -> String {
        let allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-")
        let safe = String(String.UnicodeScalarView(fw.unicodeScalars.map { allowed.contains($0) ? $0 : "_" }))
        return "\(device)-\(safe.isEmpty ? "unknown" : safe)-\(id).bin"
    }
}
