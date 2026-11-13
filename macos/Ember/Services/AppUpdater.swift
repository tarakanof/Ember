import Foundation
import Observation
import Sparkle
import EmberKit

@MainActor
@Observable
final class AppUpdater {
    let buildChecksAutomatically: Bool
    private(set) var canCheckForUpdates = false
    private(set) var checksAutomatically = false
    private(set) var waitingUpdate: String?

    @ObservationIgnored private let controller: SPUStandardUpdaterController
    @ObservationIgnored private let gate: BackgroundCheckGate
    @ObservationIgnored private let reminders = UpdateReminders()
    @ObservationIgnored private var observations: [NSKeyValueObservation] = []

    init(bundle: Bundle = .main) {
        buildChecksAutomatically = Self.buildChecksAutomatically(bundle)
        gate = BackgroundCheckGate(allowed: buildChecksAutomatically)
        controller = SPUStandardUpdaterController(startingUpdater: false, updaterDelegate: gate, userDriverDelegate: reminders)
        reminders.onChange = { [weak self] version in self?.waitingUpdate = version }
    }

    func start(defaults: UserDefaults = .standard) {
        guard observations.isEmpty else { return }
        let updater = controller.updater
        if let carried = LegacyUpdatePreference.take(from: defaults) {
            updater.automaticallyChecksForUpdates = carried
        }
        controller.startUpdater()
        canCheckForUpdates = updater.canCheckForUpdates
        checksAutomatically = updater.automaticallyChecksForUpdates
        observations = [
            updater.observe(\.canCheckForUpdates) { [weak self] updater, _ in
                MainActor.assumeIsolated { self?.canCheckForUpdates = updater.canCheckForUpdates }
            },
            updater.observe(\.automaticallyChecksForUpdates) { [weak self] updater, _ in
                MainActor.assumeIsolated { self?.checksAutomatically = updater.automaticallyChecksForUpdates }
            },
        ]
    }

    func setChecksAutomatically(_ on: Bool) {
        controller.updater.automaticallyChecksForUpdates = on
        checksAutomatically = on
    }

    func checkForUpdates() {
        controller.checkForUpdates(nil)
    }

    private static func buildChecksAutomatically(_ bundle: Bundle) -> Bool {
        switch bundle.object(forInfoDictionaryKey: "SUEnableAutomaticChecks") {
        case let flag as Bool: flag
        case let text as String: (text as NSString).boolValue
        default: true
        }
    }
}

private final class UpdateReminders: NSObject, SPUStandardUserDriverDelegate {
    var onChange: ((String?) -> Void)?

    var supportsGentleScheduledUpdateReminders: Bool { true }

    func standardUserDriverWillHandleShowingUpdate(_ handleShowingUpdate: Bool, forUpdate update: SUAppcastItem,
                                                   state: SPUUserUpdateState) {
        guard !state.userInitiated else { return }
        onChange?(update.displayVersionString)
    }

    func standardUserDriverDidReceiveUserAttention(forUpdate update: SUAppcastItem) {
        onChange?(nil)
    }

    func standardUserDriverWillFinishUpdateSession() {
        onChange?(nil)
    }
}

private final class BackgroundCheckGate: NSObject, SPUUpdaterDelegate {
    let allowed: Bool

    init(allowed: Bool) {
        self.allowed = allowed
    }

    func updater(_ updater: SPUUpdater, mayPerform updateCheck: SPUUpdateCheck) throws {
        guard updateCheck == .updatesInBackground, !allowed else { return }
        throw NSError(domain: "com.ember.Ember.updates", code: 1, userInfo: [
            NSLocalizedDescriptionKey: "This build checks for updates only when asked.",
        ])
    }
}
