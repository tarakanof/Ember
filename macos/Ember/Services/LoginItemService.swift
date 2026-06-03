import Foundation
import ServiceManagement

/// Launch-at-login via SMAppService.mainApp — the proper Login Item for an
/// LSUIElement app (shows under System Settings → General → Login Items →
/// "Open at Login"). Requires the app to be signed + in /Applications to fully
/// register; first registration may report .requiresApproval.
enum LoginItemService {
    static var isEnabled: Bool { SMAppService.mainApp.status == .enabled }

    static var statusText: String {
        switch SMAppService.mainApp.status {
        case .enabled: return "On"
        case .requiresApproval: return "Needs approval in System Settings → Login Items"
        case .notRegistered: return "Off"
        case .notFound: return "Unavailable (run the app from /Applications)"
        @unknown default: return "Unknown"
        }
    }

    /// Returns an error message on failure, nil on success.
    static func setEnabled(_ on: Bool) -> String? {
        do {
            if on { try SMAppService.mainApp.register() }
            else { try SMAppService.mainApp.unregister() }
            return nil
        } catch {
            return error.localizedDescription
        }
    }
}
