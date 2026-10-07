import Foundation

public struct KnobCoredump: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var size: Int
    public var fw: String
    public var receivedAt: Date
    public var reason: String
    public var task: String
    public var pc: String
    public var elf: String

    enum CodingKeys: String, CodingKey {
        case id, size, fw, reason, task, pc, elf
        case receivedAt = "received_at"
    }

    public init(id: String, size: Int, fw: String = "", receivedAt: Date,
                reason: String = "", task: String = "", pc: String = "", elf: String = "") {
        self.id = id; self.size = size; self.fw = fw; self.receivedAt = receivedAt
        self.reason = reason; self.task = task; self.pc = pc; self.elf = elf
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
        elf = try c.decodeIfPresent(String.self, forKey: .elf) ?? ""
    }

    public func filename(device: String) -> String {
        let allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-")
        let safe = String(String.UnicodeScalarView(fw.unicodeScalars.map { allowed.contains($0) ? $0 : "_" }))
        return "\(device)-\(safe.isEmpty ? "unknown" : safe)-\(id).bin"
    }

    public func firmware(images: [KnobFirmwareImage]) -> KnobCoredumpFirmware {
        let crashed = elf.isEmpty ? nil : images.first { $0.build == elf }?.version
        let uploader = fw.isEmpty || fw == crashed ? nil : fw
        return KnobCoredumpFirmware(crashed: crashed, uploadedBy: uploader)
    }
}

public struct KnobCoredumpFirmware: Equatable, Sendable {
    public var crashed: String?
    public var uploadedBy: String?

    public init(crashed: String?, uploadedBy: String?) {
        self.crashed = crashed
        self.uploadedBy = uploadedBy
    }
}
