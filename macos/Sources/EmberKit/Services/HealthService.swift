import Foundation

/// GET /v1/clock/health (open; cached server-side for 30 s).
public struct HealthService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func clockHealth() async throws -> ClockHealth { try await client.get("/v1/clock/health") }
}
