import Foundation

/// Ephemeral save status for a settings pane (drives a transient caption).
public enum SaveState: Equatable, Sendable {
    case idle
    case saving
    case saved
    case error(String)
}

/// Coalesces rapid edits into a single deferred write.
@MainActor
public final class DebouncedWriter {
    private var task: Task<Void, Never>?
    private let delay: Duration
    private let sleep: @Sendable (Duration) async throws -> Void

    public convenience init(delay: Duration = .milliseconds(600)) {
        self.init(delay: delay, sleep: { try await Task.sleep(for: $0) })
    }

    init(delay: Duration, sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.delay = delay
        self.sleep = sleep
    }

    public func schedule(_ action: @escaping @Sendable () async -> Void) {
        task?.cancel()
        let delay = self.delay
        let sleep = self.sleep
        task = Task {
            try? await sleep(delay)
            if Task.isCancelled { return }
            await action()
        }
    }

    public func cancel() { task?.cancel(); task = nil }
}
