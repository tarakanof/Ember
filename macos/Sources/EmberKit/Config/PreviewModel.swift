import Foundation
import Observation

/// The pixel preview at the top of a Settings pane.
@MainActor
@Observable
public final class PreviewModel {
    /// The last good preview; kept after a failure.
    public private(set) var response: PreviewResponse?
    /// Why the latest request failed; nil after a success or before any.
    public private(set) var error: FeedError?

    /// Failed with nothing to show.
    public var isUnavailable: Bool { response == nil && error != nil }

    /// The frame for one card, when the preview has it.
    public func frame(_ card: String) -> CardFrame? {
        response?.frames.first { $0.card == card }
    }

    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var task: Task<Void, Never>?
    @ObservationIgnored private var generation = 0

    public convenience init(debounce: Duration = .milliseconds(300)) {
        self.init(debounce: debounce, sleep: { try await Task.sleep(for: $0) })
    }

    init(debounce: Duration, sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.debounce = debounce
        self.sleep = sleep
    }

    /// Fetches after the debounce, superseding any pending or in-flight request.
    public func request(_ fetch: @escaping @MainActor () async throws -> PreviewResponse) {
        cancel()
        let current = generation
        let wait = debounce
        task = Task { [weak self] in
            do { try await self?.sleep(wait) } catch { return }
            guard let self, current == self.generation else { return }
            do {
                let response = try await fetch()
                guard current == self.generation else { return }
                self.response = response
                self.error = nil
            } catch {
                guard current == self.generation else { return }
                self.error = FeedError(error)
            }
            self.task = nil
        }
    }

    /// Drops the pending or in-flight request; its outcome is never applied.
    public func cancel() {
        generation += 1
        task?.cancel()
        task = nil
    }
}
