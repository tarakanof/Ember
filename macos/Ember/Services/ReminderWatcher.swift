import EventKit
import Foundation
import Observation
import EmberKit

/// Apple Reminders → clock bell-popup. The scheduling lives in EmberKit's
/// `ReminderScheduler`; this keeps the platform side: the EventKit source and
/// its authorization, prefs persisted in UserDefaults, and the App Nap
/// assertion held while the scheduler runs.
@MainActor
@Observable
public final class ReminderWatcher {
    @ObservationIgnored private let source: EventKitReminderSource
    @ObservationIgnored private let scheduler: ReminderScheduler

    public var prefs: ReminderPrefs {
        get { scheduler.prefs }
        set {
            ReminderWatcher.save(newValue)
            scheduler.prefs = newValue
        }
    }
    /// Next few upcoming due-timed reminders, for the tab's sanity-check list.
    public var upcoming: [DueReminder] { scheduler.upcoming }
    /// Why the last fire request failed, or nil once one succeeds.
    public var lastFireError: String? { scheduler.lastFireError }

    public init(client: APIClient) {
        let source = EventKitReminderSource()
        let appNap = AppNapAssertion()
        self.source = source
        scheduler = ReminderScheduler(source: source, client: client, prefs: ReminderWatcher.load(),
                                      onRunningChange: { appNap.hold($0) })
    }

    public var authorization: EKAuthorizationStatus { source.authorization }

    /// Observable mirror of `authorization` for the UI — the raw EventKit status is
    /// a non-observable global, so without this the Reminders tab wouldn't update
    /// after the user grants/denies access. Refreshed on grant + when the tab appears.
    public private(set) var authStatus: EKAuthorizationStatus = EKEventStore.authorizationStatus(for: .reminder)

    public func refreshAuthorization() {
        authStatus = source.authorization
    }

    /// Rebuilds against a new client (Connection-tab change), like the other services.
    public func reconfigure(client: APIClient) {
        scheduler.configure(client: client)
    }

    public func start() { scheduler.sync() }

    @discardableResult
    public func requestAccess() async -> EKAuthorizationStatus {
        await source.requestAccess()
        refreshAuthorization()
        scheduler.sync()
        return authStatus
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

/// Holds the App Nap assertion while the scheduler runs. An idle LSUIElement
/// app gets napped, which throttles the scheduler's sleep so reminders miss
/// their fire window (they ring on phone/Mac but not the clock).
/// `AllowingIdleSystemSleep` keeps "rings only while the Mac is awake": we
/// defeat App Nap but never block system sleep.
@MainActor
private final class AppNapAssertion {
    private var activity: NSObjectProtocol?

    func hold(_ running: Bool) {
        if running, activity == nil {
            activity = ProcessInfo.processInfo.beginActivity(
                options: .userInitiatedAllowingIdleSystemSleep,
                reason: "Watching Apple Reminders")
        } else if !running, let a = activity {
            ProcessInfo.processInfo.endActivity(a)
            activity = nil
        }
    }
}
