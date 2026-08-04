import AppKit
import Foundation

/// Opens System Settings at the Local Network privacy pane so the user can
/// enable Ember. Both Bonjour browsers (server discovery and clock discovery)
/// are dead without that grant, and macOS doesn't reliably signal a denial.
func openLocalNetworkSettings() {
    if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork") {
        NSWorkspace.shared.open(url)
    }
}
