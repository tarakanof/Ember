import Foundation

/// Typed wrapper over GET/PUT /v1/weather/config.
public struct WeatherService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func getConfig() async throws -> WeatherConfig { try await client.get("/v1/weather/config") }
    public func putConfig(_ cfg: WeatherConfig) async throws { try await client.put("/v1/weather/config", body: cfg) }
}
