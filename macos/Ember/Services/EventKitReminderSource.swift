import EventKit
import Foundation
import EmberKit

/// The production `ReminderSource`: incomplete Apple Reminders with a due time,
/// read through the app's one `EKEventStore`. Never mutates reminders.
///
/// Deliberately not MainActor-isolated. EventKit invokes the `fetchReminders`
/// completion on its OWN background queue (com.apple.eventkit.reminders.search).
/// If the fetch were MainActor-isolated, Swift would infer the completion
/// closure as @MainActor too and insert an executor-isolation check at its
/// entry, which traps (SIGTRAP) when EventKit runs it off the main queue.
/// `@unchecked Sendable`: EventKit serialises its own fetch work on its
/// internal queue, so sharing the one store across actors is the standard
/// EventKit pattern.
final class EventKitReminderSource: ReminderSource, @unchecked Sendable {
    private let store = EKEventStore()

    var authorization: EKAuthorizationStatus {
        EKEventStore.authorizationStatus(for: .reminder)
    }

    var hasAccess: Bool { authorization == .fullAccess }

    /// Shows the system prompt the first time; a failure just leaves access off.
    func requestAccess() async {
        do { _ = try await store.requestFullAccessToReminders() } catch { }
    }

    func dueTimedReminders() async -> [DueReminder] {
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

    /// The due date, only when it carries a time (a date-only reminder is
    /// never "due" at a moment).
    private static func dueDate(_ r: EKReminder) -> Date? {
        guard let comps = r.dueDateComponents, comps.hour != nil else { return nil }
        return Calendar.current.date(from: comps)
    }
}
