import SwiftUI

extension View {
    /// Keeps this view usable inside a disabled row: an info button must
    /// still open when its control is off, since that's when people need it
    /// (#290). A child's `.disabled(false)` can't undo a parent's
    /// `.disabled(true)`; overriding the environment value can.
    public func staysEnabled() -> some View {
        environment(\.isEnabled, true)
    }
}
