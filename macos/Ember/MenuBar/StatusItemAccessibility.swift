import AppKit
import os

/// Sets the menu-bar status item's accessibility value by hand.
///
/// `MenuBarExtra` passes the label view's `accessibilityLabel` to its
/// `NSStatusBarButton` (as AXTitle) but drops `accessibilityValue`, so
/// VoiceOver heard "Ember" with no state. The value is set on the button
/// directly; SwiftUI re-rendering the icon doesn't reset it (checked against
/// the AX tree: AXValue survives image swaps).
@MainActor
enum StatusItemAccessibility {
    private static let log = Logger(subsystem: "com.ember.Ember", category: "accessibility")

    /// Sets `value` on Ember's status item button. False when no window holds
    /// one yet (early in launch), so the caller can retry.
    @discardableResult
    static func setValue(_ value: String) -> Bool {
        // Ember has exactly one status item: the MenuBarExtra. Walking every
        // window avoids depending on the status bar window's private class.
        for window in NSApp.windows {
            if let button = statusButton(in: window.contentView) {
                if button.accessibilityValue() as? String != value { button.setAccessibilityValue(value) }
                return true
            }
        }
        return false
    }

    /// Retries `setValue` while the status item is being created, and logs
    /// if it never appears (a macOS change would otherwise silently bring
    /// back a menu-bar icon VoiceOver can't read).
    static func setValueWhenReady(_ value: String) async {
        for _ in 0..<20 {
            if setValue(value) || Task.isCancelled { return }
            try? await Task.sleep(for: .milliseconds(250))
        }
        if !Task.isCancelled {
            log.error("status item button not found; VoiceOver won't hear the menu-bar state")
        }
    }

    private static func statusButton(in view: NSView?) -> NSStatusBarButton? {
        guard let view else { return nil }
        if let button = view as? NSStatusBarButton { return button }
        for sub in view.subviews {
            if let button = statusButton(in: sub) { return button }
        }
        return nil
    }
}
