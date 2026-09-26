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
    /// Why the last `setApp` failed, or nil once one succeeds.
    public private(set) var appToggleError: String?

    private var status: StatusService?
    private var pomodoro: PomodoroService?
    private var appsService: AppsService?
    private var pollTask: Task<Void, Never>?
    /// Bumped by `configure` and by each `refresh`. A refresh applies its
    /// results only while it is still the latest, so the poll loop, menu
    /// actions and a Connection-tab reload interleaving across `await`s can't
    /// let a slow, older response (possibly from the previous server)
    /// overwrite a newer one.
    private var generation = 0

    public init() {}

    public func configure(client: APIClient) {
        generation += 1
        status = StatusService(client: client)
        pomodoro = PomodoroService(client: client)
        appsService = AppsService(client: client)
    }

    /// One refresh cycle. Each call is independent and non-fatal: a /state
    /// failure marks disconnected and clears the live fields rather than
    /// throwing. The pomodoro endpoints 404 while that feature is disabled on
    /// the server — that only blanks the timer/stats, never connectedness.
    /// Results are dropped if a newer refresh or `configure` started meanwhile.
    public func refresh() async {
        guard let status, let pomodoro else { return }
        generation += 1
        let mine = generation
        let appsService = self.appsService
        async let snap = status.fetchSnapshot()
        async let ps = pomodoro.state()
        async let st = pomodoro.stats()
        let newPomo = try? await ps
        let newStats = try? await st
        let snapshot = try? await snap
        var newApps: [AppToggle]?
        if snapshot != nil, let appsService { newApps = try? await appsService.list() }
        guard mine == generation else { return }

        guard let snapshot else {
            connected = false
            sessions = []
            winningSession = nil
            pomoState = nil
            stats = nil
            return
        }
        pomoState = newPomo
        stats = newStats
        sessions = snapshot.sessions
        winningSession = pickWinning(snapshot.sessions)
        if let newApps { apps = newApps }
        connected = true
    }

    /// Toggle an app's clock visibility, then refresh so the list reflects it.
    /// A failure is kept in `appToggleError` (the toggle snaps back on refresh).
    public func setApp(_ name: String, enabled: Bool) async {
        guard let appsService else { return }
        do {
            try await appsService.set(name, enabled: enabled)
            appToggleError = nil
        } catch {
            appToggleError = error.localizedDescription
        }
        await refresh()
    }

    /// Starts a poll loop every `interval` seconds until stop(). Safe to call once.
    public func startPolling(interval: Duration = .seconds(3)) {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.refresh()
                try? await Task.sleep(for: interval)
            }
        }
    }

    public func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }
}
