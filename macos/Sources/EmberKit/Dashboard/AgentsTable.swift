import Foundation

/// The Agents card: sessions in table order plus the header counts.
public struct AgentsTable: Equatable, Sendable {
    /// A session with a stable identity for `Table`.
    public struct Row: Equatable, Sendable, Identifiable {
        public let session: Session
        /// Source, tool and session id: what the server keys sessions by.
        public var id: String { "\(session.source)|\(session.tool)|\(session.session)" }
    }

    public let rows: [Row]
    public let running: Int
    public let waiting: Int

    public var isEmpty: Bool { rows.isEmpty }

    /// What needs attention first (`Session.State.sortRank`), then the most
    /// recently updated; ties keep a stable order by source and session id.
    public init(sessions: [Session]) {
        rows = sessions.sorted { a, b in
            let ra = a.stateEnum.sortRank, rb = b.stateEnum.sortRank
            if ra != rb { return ra < rb }
            if a.updatedAt != b.updatedAt { return a.updatedAt > b.updatedAt }
            return (a.source, a.session) < (b.source, b.session)
        }.map(Row.init)
        running = sessions.filter { $0.stateEnum == .running }.count
        waiting = sessions.filter { $0.stateEnum == .waiting }.count
    }
}
