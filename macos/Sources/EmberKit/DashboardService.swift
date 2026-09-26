import Foundation

/// Typed wrapper over the dashboard read endpoints. All of them are
/// unauthenticated on the server; the client still sends its token if it has one.
public struct DashboardService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func usage() async throws -> UsageSnapshot { try await client.get("/v1/usage") }

    /// `days` is clamped server-side to 1...90.
    public func activitySummary(days: Int = 7) async throws -> ActivitySummary {
        try await client.get("/v1/activity/summary", query: [URLQueryItem(name: "days", value: String(days))])
    }

    public func weatherState() async throws -> WeatherState { try await client.get("/v1/weather/state") }

    public func clockHealth() async throws -> ClockHealth { try await client.get("/v1/clock/health") }

    /// `days` is clamped server-side to 1...90.
    public func workHours(days: Int = 14) async throws -> WorkHours {
        try await client.get("/v1/pomodoro/workhours", query: [URLQueryItem(name: "days", value: String(days))])
    }
}
