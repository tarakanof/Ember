import SwiftUI

extension View {
    /// Override the environment: a child's `.disabled(false)` can't undo a parent's (ARCHITECTURE gotchas).
    public func staysEnabled() -> some View {
        environment(\.isEnabled, true)
    }
}
