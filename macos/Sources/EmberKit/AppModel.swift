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
    public private(set) var apps: [AppToggle] = []

    private var status: StatusService?
    private var pomodoro: PomodoroService?
    private var appsService: AppsService?
    private var pollTask: Task<Void, Never>?

    public init() {}

    public func configure(client: APIClient) {
        status = StatusService(client: client)
        pomodoro = PomodoroService(client: client)
        appsService = AppsService(client: client)
    }

    /// One refresh cycle. Each call is independent and non-fatal: a /state
    /// failure marks disconnected and clears the live fields rather than
    /// throwing. The pomodoro endpoints 404 while that feature is disabled on
    /// the server — that only blanks the timer/stats, never connectedness.
    public func refresh() async {
        guard let status, let pomodoro else { return }
        async let snap = status.fetchSnapshot()
        async let ps = pomodoro.state()
        async let st = pomodoro.stats()
        pomoState = try? await ps
        stats = try? await st
        do {
            let snapshot = try await snap
            sessions = snapshot.sessions
            winningSession = pickWinning(snapshot.sessions)
            if let appsService { apps = (try? await appsService.list()) ?? apps }
            connected = true
        } catch {
            connected = false
            sessions = []
            winningSession = nil
            pomoState = nil
            stats = nil
        }
    }

    /// Toggle an app's clock visibility, then refresh so the list reflects it.
    public func setApp(_ name: String, enabled: Bool) async {
        guard let appsService else { return }
        try? await appsService.set(name, enabled: enabled)
        await refresh()
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
