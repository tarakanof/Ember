import EventKit
import Foundation
import Observation
import EmberKit

@MainActor
@Observable
public final class ReminderWatcher {
    @ObservationIgnored private let source: EventKitReminderSource
    @ObservationIgnored private let scheduler: ReminderScheduler
    @ObservationIgnored private let prefsSync: ReminderPrefsSync

    public var prefs: ReminderPrefs? { scheduler.prefs }
    public var upcoming: [DueReminder] { scheduler.upcoming }
    public var lastFireError: String? { scheduler.lastFireError }

    public init(client: APIClient, settings: SettingsModels) {
        let source = EventKitReminderSource()
        let appNap = AppNapAssertion()
        self.source = source
        let scheduler = ReminderScheduler(source: source, client: client, prefs: nil,
                                          onArmedChange: { appNap.hold($0) })
        self.scheduler = scheduler
        prefsSync = ReminderPrefsSync(model: { settings.reminders }, legacy: .userDefaults(),
                                      saveIfDefault: { try await settings.saveRemindersIfDefault($0) },
                                      apply: { scheduler.prefs = $0 })
    }

    public var authorization: EKAuthorizationStatus { source.authorization }

    public private(set) var authStatus: EKAuthorizationStatus = EKEventStore.authorizationStatus(for: .reminder)

    public func refreshAuthorization() {
        authStatus = source.authorization
    }

    public func reconfigure(client: APIClient) {
        scheduler.configure(client: client)
    }

    public func start() {
        scheduler.sync()
        prefsSync.start()
    }

    @discardableResult
    public func requestAccess() async -> EKAuthorizationStatus {
        await source.requestAccess()
        refreshAuthorization()
        scheduler.sync()
        return authStatus
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
