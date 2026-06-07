import EventKit
import Foundation
import Observation
import EmberKit

/// Watches Apple Reminders and fires the clock bell-popup when a reminder with a
/// due time comes due. Runs only while enabled + authorized; settings persist in
/// UserDefaults. Polls every `pollSeconds`; the fire-window grace covers a missed
/// poll. Never mutates the user's reminders.
@MainActor
@Observable
public final class ReminderWatcher {
    private let store = EKEventStore()
    private var loop: Task<Void, Never>?
    private var fired = Set<String>()

    private let pollSeconds: UInt64 = 30
    private let grace: TimeInterval = 90

    public var prefs: ReminderPrefs {
        didSet {
            ReminderWatcher.save(prefs)
            applyEnabled()
        }
    }
    public private(set) var client: APIClient
    /// Next few upcoming due-timed reminders, for the tab's sanity-check list.
    public private(set) var upcoming: [UpcomingReminder] = []

    public struct UpcomingReminder: Identifiable, Equatable {
        public let id: String
        public let title: String
        public let due: Date
    }

    public init(client: APIClient) {
        self.client = client
        self.prefs = ReminderWatcher.load()
    }

    public var authorization: EKAuthorizationStatus {
        EKEventStore.authorizationStatus(for: .reminder)
    }

    /// Observable mirror of `authorization` for the UI — the raw EventKit status is
    /// a non-observable global, so without this the Reminders tab wouldn't update
    /// after the user grants/denies access. Refreshed on grant + when the tab appears.
    public private(set) var authStatus: EKAuthorizationStatus = EKEventStore.authorizationStatus(for: .reminder)

    public func refreshAuthorization() {
        authStatus = EKEventStore.authorizationStatus(for: .reminder)
    }

    /// Rebuilds against a new client (Connection-tab change), like the other services.
    public func reconfigure(client: APIClient) {
        self.client = client
    }

    public func start() { applyEnabled() }

    @discardableResult
    public func requestAccess() async -> EKAuthorizationStatus {
        do { _ = try await store.requestFullAccessToReminders() } catch { }
        refreshAuthorization()
        applyEnabled()
        return authStatus
    }

    private func applyEnabled() {
        let shouldRun = prefs.enabled && authorization == .fullAccess
        if shouldRun, loop == nil {
            loop = Task { [weak self] in
                while !Task.isCancelled {
                    await self?.poll()
                    try? await Task.sleep(nanoseconds: (self?.pollSeconds ?? 30) * 1_000_000_000)
                }
            }
        } else if !shouldRun, let l = loop {
            l.cancel(); loop = nil
        }
    }

    /// A Sendable snapshot of a due-timed reminder, extracted off the EventKit
    /// callback queue so nothing non-Sendable crosses the actor hop.
    private struct Snapshot: Sendable {
        let id: String
        let title: String
        let due: Date
    }

    private func poll() async {
        guard prefs.enabled, authorization == .fullAccess else { return }
        let now = Date()
        let reminders = await fetchIncompleteDueTimed()
        upcoming = reminders
            .map { UpcomingReminder(id: $0.id, title: $0.title, due: $0.due) }
            .filter { $0.due >= now }
            .sorted { $0.due < $1.due }
            .prefix(5).map { $0 }

        for r in reminders {
            let due = r.due
            guard reminderShouldFire(now: now, dueDate: due, leadMinutes: prefs.leadMinutes, grace: grace) else { continue }
            let key = reminderDedupeKey(id: r.id, dueDate: due)
            if fired.contains(key) { continue }
            let title = r.title.trimmingCharacters(in: .whitespacesAndNewlines)
            if title.isEmpty { continue }
            fired.insert(key)
            await fire(title: title)
        }
    }

    private func fire(title: String) async {
        let svc = RemindersService(client: client)
        do {
            try await svc.fire(text: title, sound: prefs.sound, duration: prefs.popupDuration,
                               nativeIconId: prefs.useNativeIcon ? prefs.nativeIconId : "")
        } catch {
            NSLog("Ember reminder fire failed: \(error)")
        }
    }

    // `nonisolated` is REQUIRED: this runs inside EventKit's fetchReminders
    // completion block, which executes on a background dispatch queue. Without it
    // the call would assert MainActor isolation off the main queue and SIGTRAP.
    nonisolated private static func dueDate(_ r: EKReminder) -> Date? {
        guard let comps = r.dueDateComponents, comps.hour != nil else { return nil }
        return Calendar.current.date(from: comps)
    }

    private func fetchIncompleteDueTimed() async -> [Snapshot] {
        await withCheckedContinuation { cont in
            let pred = store.predicateForIncompleteReminders(withDueDateStarting: nil, ending: nil, calendars: nil)
            store.fetchReminders(matching: pred) { rems in
                let snaps: [Snapshot] = (rems ?? []).compactMap { r in
                    guard let due = ReminderWatcher.dueDate(r) else { return nil }
                    return Snapshot(id: r.calendarItemIdentifier, title: r.title ?? "", due: due)
                }
                cont.resume(returning: snaps)
            }
        }
    }

    private static let key = "reminderPrefs"
    static func load() -> ReminderPrefs {
        guard let data = UserDefaults.standard.data(forKey: key),
              let p = try? JSONDecoder().decode(ReminderPrefs.self, from: data) else { return ReminderPrefs() }
        return p
    }
    static func save(_ p: ReminderPrefs) {
        if let data = try? JSONEncoder().encode(p) { UserDefaults.standard.set(data, forKey: key) }
    }
}
