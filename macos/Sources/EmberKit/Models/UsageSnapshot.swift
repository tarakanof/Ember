import Foundation

// Dashboard wire models (cmd/ember/dashboard_http.go). The server emits RFC 3339
// timestamps with whole seconds, null for "no value", series as arrays of
// points, and the unit in every key, so these decode with APIClient's .iso8601
// decoder and feed Swift Charts directly. The decode tests read the server's
// own golden files (cmd/ember/testdata/dashboard).

/// GET /v1/usage — the latest subscription-usage snapshot per tool.
public struct UsageSnapshot: Decodable, Sendable, Equatable {
    /// One quota window. `resetsAt`/`resetLabel` are nil when the producer
    /// didn't report them.
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
        /// Older than `staleAfterSec`; the clock stops showing it too.
        public var stale: Bool
        public var fiveHour: Window?
        public var sevenDay: Window?
        /// Keyed by model name ("opus", "sonnet").
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
    /// Sorted by tool name.
    public var tools: [Tool]

    enum CodingKeys: String, CodingKey {
        case tools
        case generatedAt = "generated_at"
        case staleAfterSec = "stale_after_sec"
    }
}
