import Foundation
import Observation

/// Single source of UI truth, updated by refresh()/the poll loop. @MainActor so
/// SwiftUI views observe it safely; @Observable drives view updates.
@MainActor
@Observable
public final class AppModel {
    public private(set) var connected = false
    public private(set) var sessions: [Session] = []
    public private(set) var winningSession: Session?
    public private(set) var pomoState: PomoState?
    public private(set) var stats: PomoStats?

    private var status: StatusService?
    private var pomodoro: PomodoroService?
    private var pollTask: Task<Void, Never>?

    public init() {}

    public func configure(client: APIClient) {
        status = StatusService(client: client)
        pomodoro = PomodoroService(client: client)
    }

    /// One refresh cycle. Each call is independent and non-fatal: a failure marks
    /// disconnected and clears the live fields rather than throwing.
    public func refresh() async {
        guard let status, let pomodoro else { return }
        do {
            async let snap = status.fetchSnapshot()
            async let ps = pomodoro.state()
            async let st = pomodoro.stats()
            let (snapshot, pomo, stat) = try await (snap, ps, st)
            sessions = snapshot.sessions
            winningSession = pickWinning(snapshot.sessions)
            pomoState = pomo
            stats = stat
            connected = true
        } catch {
            connected = false
            sessions = []
            winningSession = nil
            pomoState = nil
            stats = nil
        }
    }

    /// Starts a poll loop every `interval` seconds until stop(). Safe to call once.
    public func startPolling(interval: Duration = .seconds(3)) {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(for: interval)
            }
        }
    }

    public func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }
}
