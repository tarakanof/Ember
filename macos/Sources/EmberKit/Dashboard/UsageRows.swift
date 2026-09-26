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

    /// The Usage card's rows: the snapshot's fresh tools, and for every
    /// other tool the `/state` session fallback the menu uses
    /// (`MenuRows.sessionFiveHour`: a known reset that hasn't passed yet).
    /// A fresh entry without a 5-hour window borrows the session's; a stale
    /// entry yields to the session row when there is one and is otherwise
    /// kept, flagged stale. `snapshot` is nil on a server without
    /// `GET /v1/usage`. Pass only live sessions (`MenuRows.liveSessions`).
    public static func rows(from snapshot: UsageSnapshot?, sessions: [Session], now: Date) -> [UsageRow] {
        var byTool: [String: UsageRow] = [:]
        for row in snapshot.map(rows(from:)) ?? [] { byTool[row.tool] = row }
        for row in rows(fromSessions: sessions, now: now) {
            guard let current = byTool[row.tool], !current.stale else {
                byTool[row.tool] = row
                continue
            }
            if current.fiveHour == nil {
                byTool[row.tool] = UsageRow(tool: current.tool, source: current.source, fiveHour: row.fiveHour,
                                            sevenDay: current.sevenDay, models: current.models,
                                            updatedAt: current.updatedAt, stale: false)
            }
        }
        return byTool.values.sorted { $0.tool < $1.tool }
    }

    /// Rows from `/state` sessions alone: each tool's 5-hour window per
    /// `MenuRows.sessionFiveHour`.
    public static func rows(fromSessions sessions: [Session], now: Date) -> [UsageRow] {
        MenuRows.sessionFiveHour(sessions, now: now).map { w in
            UsageRow(tool: w.tool, source: w.session.source.isEmpty ? nil : w.session.source,
                     fiveHour: Window(percent: Double(w.percent), resetsAt: w.resetsAt, resetLabel: nil),
                     sevenDay: nil, models: [], updatedAt: w.session.updatedAt, stale: false)
        }
    }
}
