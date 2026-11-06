import AppKit
import SwiftUI

struct WindowVisibilityReader: NSViewRepresentable {
    @Binding var isVisible: Bool

    func makeNSView(context: Context) -> NSView {
        let view = ProbeView()
        view.onChange = { visible in
            if isVisible != visible { isVisible = visible }
        }
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {}

    final class ProbeView: NSView {
        var onChange: ((Bool) -> Void)?

        override func viewWillMove(toWindow newWindow: NSWindow?) {
            super.viewWillMove(toWindow: newWindow)
            NotificationCenter.default.removeObserver(self, name: NSWindow.didChangeOcclusionStateNotification, object: nil)
        }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard let window else { return }
            NotificationCenter.default.addObserver(self, selector: #selector(occlusionChanged(_:)),
                                                   name: NSWindow.didChangeOcclusionStateNotification, object: window)
            onChange?(!window.isVisible || window.occlusionState.contains(.visible))
        }

        @objc private func occlusionChanged(_ note: Notification) {
            guard let window else { return }
            onChange?(window.occlusionState.contains(.visible))
        }
    }
}
