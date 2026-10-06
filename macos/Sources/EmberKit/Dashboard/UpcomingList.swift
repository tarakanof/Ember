import Foundation

public struct UpcomingItem: Equatable, Sendable, Identifiable {
    public enum Kind: Hashable, Sendable { case meeting, reminder }

    public let id: String
    public let kind: Kind
    public let title: String
    public let date: Date

    public init(id: String, kind: Kind, title: String, date: Date) {
        self.id = id
        self.kind = kind
        self.title = title
        self.date = date
    }

    public static let horizon: TimeInterval = 36 * 3600
    public static let limit = 5
    public static let relativeBelow: TimeInterval = 3600

    public static func merge(meetings: [MeetingsState.Item], reminders: [UpcomingItem],
                             now: Date) -> [UpcomingItem] {
        let from = now.addingTimeInterval(-5 * 60), to = now.addingTimeInterval(horizon)
        let m = meetings.map { UpcomingItem(id: "m|\($0.id)", kind: .meeting, title: $0.title, date: $0.start) }
        return (m + reminders)
            .filter { $0.date >= from && $0.date <= to }
            .sorted { ($0.date, $0.title) < ($1.date, $1.title) }
            .prefix(limit)
            .map { $0 }
    }
}
