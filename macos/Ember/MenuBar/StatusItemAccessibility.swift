import AppKit
import os

@MainActor
enum StatusItemAccessibility {
    private static let log = Logger(subsystem: "com.ember.Ember", category: "accessibility")

    @discardableResult
    static func setValue(_ value: String) -> Bool {
        guard let button = StatusItemButton.find() else { return false }
        if button.accessibilityValue() as? String != value { button.setAccessibilityValue(value) }
        return true
    }

    static func setValueWhenReady(_ value: String) async {
        for _ in 0..<20 {
            if setValue(value) || Task.isCancelled { return }
            try? await Task.sleep(for: .milliseconds(250))
        }
        if !Task.isCancelled {
            log.error("status item button not found; VoiceOver won't hear the menu-bar state")
        }
    }
}

@MainActor
enum StatusItemButton {
    static func find() -> NSStatusBarButton? {
        for window in NSApp.windows {
            if let button = find(in: window.contentView) { return button }
        }
        return nil
    }

    private static func find(in view: NSView?) -> NSStatusBarButton? {
        guard let view else { return nil }
        if let button = view as? NSStatusBarButton { return button }
        for sub in view.subviews {
            if let button = find(in: sub) { return button }
        }
        return nil
    }
}
