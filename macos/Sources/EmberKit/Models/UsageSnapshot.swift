import Foundation

public struct UsageSnapshot: Decodable, Sendable, Equatable {
    public struct Window: Decodable, Sendable, Equatable {
        public var usedPercent: Double
        public var resetsAt: Date?
        public var resetLabel: String?

        enum CodingKeys: String, CodingKey {
            case usedPercent = "used_percent"
            case resetsAt = "resets_at"
            case resetLabel = "reset_label"
        }
    }

    public struct Tool: Decodable, Sendable, Equatable, Identifiable {
        public var tool: String
        public var source: String?
        public var updatedAt: Date
        public var stale: Bool
        public var fiveHour: Window?
        public var sevenDay: Window?
        public var models: [String: Window]
        public var id: String { tool }

        enum CodingKeys: String, CodingKey {
            case tool, source, stale, models
            case updatedAt = "updated_at"
            case fiveHour = "five_hour"
            case sevenDay = "seven_day"
        }
    }

    public var generatedAt: Date
    public var staleAfterSec: Int
    public var tools: [Tool]

    enum CodingKeys: String, CodingKey {
        case tools
        case generatedAt = "generated_at"
        case staleAfterSec = "stale_after_sec"
    }
}
