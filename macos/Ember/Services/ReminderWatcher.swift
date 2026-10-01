import EventKit
import Foundation
import Observation
import EmberKit

/// Bridges Apple Reminders to the clock's bell popup.
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
    public var upcoming: [DueReminder] { scheduler.upcoming }
    public var lastFireError: String? { scheduler.lastFireError }

    public init(client: APIClient) {
        let source = EventKitReminderSource()
        let appNap = AppNapAssertion()
        self.source = source
        scheduler = ReminderScheduler(source: source, client: client, prefs: ReminderWatcher.load(),
                                      onArmedChange: { appNap.hold($0) })
    }

    public var authorization: EKAuthorizationStatus { source.authorization }

    public private(set) var authStatus: EKAuthorizationStatus = EKEventStore.authorizationStatus(for: .reminder)

    public func refreshAuthorization() {
        authStatus = source.authorization
    }

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

@MainActor
private final class AppNapAssertion {
    private var activity: NSObjectProtocol?

    func hold(_ armed: Bool) {
        if armed, activity == nil {
            activity = ProcessInfo.processInfo.beginActivity(
                options: .userInitiatedAllowingIdleSystemSleep,
                reason: "Watching Apple Reminders")
        } else if !armed, let a = activity {
            ProcessInfo.processInfo.endActivity(a)
            activity = nil
        }
    }
}
