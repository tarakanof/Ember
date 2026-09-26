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
    /// A snapshot 5-hour window whose reset has passed is over and counts as
    /// none (as in the menu). An entry without a 5-hour window, or a stale
    /// one, takes the session's; with the session live the row is no longer
    /// flagged stale, but its 7-day window and models are kept as they were
    /// (they move slowly). A stale entry with no session window stays,
    /// flagged. `snapshot` is nil on a server without `GET /v1/usage`. Pass
    /// only live sessions (`MenuRows.liveSessions`).
    public static func rows(from snapshot: UsageSnapshot?, sessions: [Session], now: Date) -> [UsageRow] {
        var byTool: [String: UsageRow] = [:]
        for row in snapshot.map(rows(from:)) ?? [] {
            let over = row.fiveHour?.resetsAt.map { $0 <= now } ?? false
            byTool[row.tool] = over ? row.with(fiveHour: nil) : row
        }
        for session in rows(fromSessions: sessions, now: now) {
            guard let current = byTool[session.tool] else {
                byTool[session.tool] = session
                continue
            }
            if current.stale || current.fiveHour == nil {
                byTool[session.tool] = current.with(fiveHour: session.fiveHour, stale: false)
            }
        }
        return byTool.values
            .filter { $0.fiveHour != nil || $0.sevenDay != nil || !$0.models.isEmpty }
            .sorted { $0.tool < $1.tool }
    }

    private func with(fiveHour: Window?, stale: Bool? = nil) -> UsageRow {
        UsageRow(tool: tool, source: source, fiveHour: fiveHour, sevenDay: sevenDay, models: models,
                 updatedAt: updatedAt, stale: stale ?? self.stale)
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
