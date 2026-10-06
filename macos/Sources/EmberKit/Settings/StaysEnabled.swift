import SwiftUI

extension View {
    /// A child's `.disabled(false)` can't undo a parent's `.disabled(true)`; overriding the environment value can.
    public func staysEnabled() -> some View {
        environment(\.isEnabled, true)
    }
}
