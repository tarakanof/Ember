import AppKit

/// Sets the menu-bar status item's accessibility value by hand.
///
/// `MenuBarExtra` passes the label view's `accessibilityLabel` to its
/// `NSStatusBarButton` (as AXTitle) but drops `accessibilityValue`, so
/// VoiceOver heard "Ember" with no state. The value is set on the button
/// directly; SwiftUI re-rendering the icon doesn't reset it (checked against
/// the AX tree: AXValue survives image swaps).
@MainActor
enum StatusItemAccessibility {
    /// Sets `value` on Ember's status item button. False when the status bar
    /// window doesn't exist yet (early in launch), so the caller can retry.
    @discardableResult
    static func setValue(_ value: String) -> Bool {
        // Ember has exactly one status item: the MenuBarExtra.
        for window in NSApp.windows where window.className == "NSStatusBarWindow" {
            if let button = statusButton(in: window.contentView) {
                if button.accessibilityValue() as? String != value { button.setAccessibilityValue(value) }
                return true
            }
        }
        return false
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
