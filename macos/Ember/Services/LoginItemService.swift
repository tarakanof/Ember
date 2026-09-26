import Foundation
import ServiceManagement

/// Launch-at-login via SMAppService.mainApp — the proper Login Item for an
/// LSUIElement app (shows under System Settings → General → Login Items →
/// "Open at Login"). Requires the app to be signed + in /Applications to fully
/// register; first registration may report .requiresApproval.
enum LoginItemService {
    static var status: SMAppService.Status { SMAppService.mainApp.status }

    static var isEnabled: Bool { status == .enabled }

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
