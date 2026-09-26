import Foundation

/// Pomodoro statistics reads. `stats()` still lives on PomodoroService; the
/// heatmap is added with the dashboard (#119).
public struct StatsService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    /// `days` is clamped server-side to 1...90.
    public func workHours(days: Int = 14) async throws -> WorkHours {
        try await client.get("/v1/pomodoro/workhours", query: [URLQueryItem(name: "days", value: String(days))])
    }
}
