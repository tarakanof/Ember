import Foundation

/// Ephemeral save status for a settings pane (drives a transient caption).
public enum SaveState: Equatable, Sendable {
    case idle
    case saving
    case saved
    case error(String)
}

/// Coalesces rapid edits into a single deferred write. macOS-14-safe (plain
/// Swift concurrency only — no SwiftUI/Observation). Each `schedule` cancels the
/// previous pending action, so a burst collapses to one run after `delay`.
@MainActor
public final class DebouncedWriter {
    private var task: Task<Void, Never>?
    private let delay: Duration

    public init(delay: Duration = .milliseconds(600)) { self.delay = delay }

    public func schedule(_ action: @escaping @Sendable () async -> Void) {
        task?.cancel()
        let delay = self.delay
        task = Task {
            try? await Task.sleep(for: delay)
            if Task.isCancelled { return }
            await action()
        }
    }

    public func cancel() { task?.cancel(); task = nil }
}
