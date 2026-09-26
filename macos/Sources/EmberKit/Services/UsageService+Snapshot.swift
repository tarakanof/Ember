import Foundation

extension UsageService {
    /// GET /v1/usage (open): the latest usage snapshot per tool.
    public func snapshot() async throws -> UsageSnapshot { try await client.get("/v1/usage") }
}
