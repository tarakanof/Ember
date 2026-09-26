import Foundation

/// GET /v1/activity/summary (open): agent time per tool and per source.
public struct ActivityService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    /// `days` is clamped server-side to 1...90.
    public func summary(days: Int = 7) async throws -> ActivitySummary {
        try await client.get("/v1/activity/summary", query: [URLQueryItem(name: "days", value: String(days))])
    }
}
