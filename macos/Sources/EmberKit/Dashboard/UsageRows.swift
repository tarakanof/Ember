import Foundation

/// The Usage card: one row per tool with its 5-hour and 7-day windows.
public struct UsageRow: Equatable, Sendable, Identifiable {
    public struct Window: Equatable, Sendable {
        /// 0...100.
        public let percent: Double
        public let resetsAt: Date?
        /// The producer's own label ("12:30") when there's no timestamp.
        public let resetLabel: String?
    }

    public struct Model: Equatable, Sendable, Identifiable {
        /// "opus".
        public let name: String
        public let percent: Double
        public var id: String { name }
    }

    /// Wire tool name ("claude").
    public let tool: String
    public let source: String?
    public let fiveHour: Window?
    public let sevenDay: Window?
    /// Highest use first.
    public let models: [Model]
    public let updatedAt: Date?
    public let stale: Bool
    public var id: String { tool }

    /// Rows from the usage snapshot (`GET /v1/usage`).
    public static func rows(from snapshot: UsageSnapshot) -> [UsageRow] {
        snapshot.tools.map { t in
            UsageRow(tool: t.tool, source: t.source,
                     fiveHour: t.fiveHour.map { Window(percent: $0.usedPercent, resetsAt: $0.resetsAt, resetLabel: $0.resetLabel) },
                     sevenDay: t.sevenDay.map { Window(percent: $0.usedPercent, resetsAt: $0.resetsAt, resetLabel: $0.resetLabel) },
                     models: t.models.map { Model(name: $0.key, percent: $0.value.usedPercent) }
                        .sorted { ($0.percent, $1.name) > ($1.percent, $0.name) },
                     updatedAt: t.updatedAt, stale: t.stale)
        }
    }

    /// Rows from `/state` sessions, for servers without `GET /v1/usage`: the
    /// 5-hour percentage each session carries, the freshest per tool. Tools
    /// whose sessions carry none are left out.
    public static func rows(fromSessions sessions: [Session]) -> [UsageRow] {
        var best: [String: Session] = [:]
        for s in sessions where s.rateWindowPct != nil && !s.tool.isEmpty {
            if let cur = best[s.tool], cur.updatedAt >= s.updatedAt { continue }
            best[s.tool] = s
        }
        return best.values.sorted { $0.tool < $1.tool }.map { s in
            let reset = s.rateResetAt > 0 ? Date(timeIntervalSince1970: TimeInterval(s.rateResetAt)) : nil
            return UsageRow(tool: s.tool, source: s.source.isEmpty ? nil : s.source,
                            fiveHour: Window(percent: Double(s.rateWindowPct ?? 0), resetsAt: reset, resetLabel: nil),
                            sevenDay: nil, models: [], updatedAt: s.updatedAt, stale: false)
        }
    }
}
