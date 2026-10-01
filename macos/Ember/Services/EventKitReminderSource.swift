import EventKit
import Foundation
import EmberKit

final class EventKitReminderSource: ReminderSource, @unchecked Sendable {
    private let store = EKEventStore()

    var authorization: EKAuthorizationStatus {
        EKEventStore.authorizationStatus(for: .reminder)
    }

    var hasAccess: Bool { authorization == .fullAccess }

    func requestAccess() async {
        do { _ = try await store.requestFullAccessToReminders() } catch { }
    }

    nonisolated func dueTimedReminders() async -> [DueReminder] {
        await withCheckedContinuation { cont in
            let pred = store.predicateForIncompleteReminders(withDueDateStarting: nil, ending: nil, calendars: nil)
            store.fetchReminders(matching: pred) { rems in
                let due: [DueReminder] = (rems ?? []).compactMap { r in
                    guard let date = Self.dueDate(r) else { return nil }
                    return DueReminder(id: r.calendarItemIdentifier, title: r.title ?? "", due: date)
                }
                cont.resume(returning: due)
            }
        }
    }

    private static func dueDate(_ r: EKReminder) -> Date? {
        guard let comps = r.dueDateComponents, comps.hour != nil else { return nil }
        return Calendar.current.date(from: comps)
    }
}
