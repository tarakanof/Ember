import SwiftUI
import EmberKit

/// Card 4: the next meetings and reminders over 36 hours.
struct UpcomingCard: View {
    let meetings: Loadable<MeetingsState>
    let reminders: [UpcomingItem]
    var meetingsEnabled: Bool?
    var now = Date()

    var body: some View {
        DashboardCard(title: "Upcoming", systemImage: "calendar") {
            FeedStateView(feed: feed, placeholder: Self.placeholder, isEmpty: \.isEmpty,
                          emptyTitle: "Nothing in the next 36 hours", emptySymbol: "calendar",
                          offTitle: "Meetings and reminders are off",
                          offSettingsPane: "calendar") { items in
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(items) { item in
                        row(item)
                        if item.id != items.last?.id { Divider().padding(.leading, 26) }
                    }
                }
            }
        }
    }

    /// Reminders are local, so a meetings failure only matters when there's
    /// nothing else to show.
    private var feed: Loadable<[UpcomingItem]> {
        let merge = { (m: MeetingsState?) in
            UpcomingItem.merge(meetings: m?.upcoming ?? [], reminders: reminders, now: now)
        }
        if meetingsEnabled == false || meetings.error == .featureOff {
            return reminders.isEmpty && meetingsEnabled == false
                ? .failed(.featureOff, last: nil, lastAt: nil)
                : .loaded(merge(nil), at: now)
        }
        if meetings.value == nil, !reminders.isEmpty { return .loaded(merge(nil), at: now) }
        return meetings.map { merge($0) }
    }

    private func row(_ item: UpcomingItem) -> some View {
        HStack(spacing: 8) {
            Image(systemName: item.kind == .meeting ? "calendar" : "checklist")
                .foregroundStyle(item.kind == .meeting ? Color.accentColor : Color.orange)
                .frame(width: 18)
                .accessibilityLabel(item.kind == .meeting ? Text("Meeting") : Text("Reminder"))
            Text(verbatim: item.title)
                .lineLimit(1)
                .truncationMode(.tail)
            Spacer(minLength: 8)
            when(item.date)
                .font(.callout)
                .foregroundStyle(item.date.timeIntervalSince(now) < UpcomingItem.relativeBelow ? .primary : .secondary)
                .monospacedDigit()
        }
        .padding(.vertical, 5)
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private func when(_ date: Date) -> some View {
        let delta = date.timeIntervalSince(now)
        if delta < 60 {
            Text("Now")
        } else if delta < UpcomingItem.relativeBelow {
            Text("in \(Int((delta / 60).rounded())) min")
        } else if Calendar.current.isDate(date, inSameDayAs: now) {
            Text(date, format: .dateTime.hour().minute())
        } else {
            Text(date, format: .dateTime.weekday(.abbreviated).hour().minute())
        }
    }

    static let placeholder: [UpcomingItem] = [
        UpcomingItem(id: "1", kind: .meeting, title: "Team standup", date: .now.addingTimeInterval(900)),
        UpcomingItem(id: "2", kind: .reminder, title: "Call the bank", date: .now.addingTimeInterval(7200)),
        UpcomingItem(id: "3", kind: .meeting, title: "One-on-one", date: .now.addingTimeInterval(14400)),
    ]
}
