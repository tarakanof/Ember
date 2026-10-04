import Foundation
import UserNotifications
import EmberKit

/// "Knob connected — Set up": posted when a cinder knob without Wi-Fi or
/// Ember settings is plugged in. Clicking it opens Settings › Knob.
@MainActor
final class KnobNotifier: NSObject, UNUserNotificationCenterDelegate {
    var onOpen: (@MainActor () -> Void)?
    nonisolated private static let category = "ember.knob.setup"

    func post(_ identity: KnobIdentity) {
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        let content = UNMutableNotificationContent()
        content.title = String(localized: "Knob connected")
        content.body = String(localized: "Set up \(identity.info.name) in Settings › Knob.",
                              comment: "Notification body when an unconfigured knob is plugged in; the argument is the knob's name (\"Knob 61FC8C\").")
        content.categoryIdentifier = Self.category
        let request = UNNotificationRequest(identifier: "ember.knob.\(identity.hwID ?? "unknown")",
                                            content: content, trigger: nil)
        Task {
            guard (try? await center.requestAuthorization(options: [.alert])) == true else { return }
            try? await center.add(request)
        }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            didReceive response: UNNotificationResponse) async {
        guard response.notification.request.content.categoryIdentifier == Self.category else { return }
        await MainActor.run { onOpen?() }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter,
                                            willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner]
    }
}
