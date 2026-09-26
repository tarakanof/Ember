import Foundation

/// Pomodoro statistics reads (all open). `PomodoroService.stats()` stays for
/// the settings panes that already call it.
public struct StatsService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    /// GET /v1/pomodoro/stats: today, the last 7 days, streaks, goals and the
    /// 12-week trend. Cached server-side for a minute.
    public func stats() async throws -> PomoStats { try await client.get("/v1/pomodoro/stats") }

    /// GET /v1/pomodoro/heatmap. `days` is clamped server-side to 7...366.
    public func heatmap(days: Int = 84) async throws -> Heatmap {
        try await client.get("/v1/pomodoro/heatmap", query: [URLQueryItem(name: "days", value: String(days))])
    }

    /// `days` is clamped server-side to 1...90.
    public func workHours(days: Int = 14) async throws -> WorkHours {
        try await client.get("/v1/pomodoro/workhours", query: [URLQueryItem(name: "days", value: String(days))])
    }
}
