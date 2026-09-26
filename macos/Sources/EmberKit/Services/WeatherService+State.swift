import Foundation

extension WeatherService {
    /// GET /v1/weather/state (open): the cached observation, no provider call.
    public func state() async throws -> WeatherState { try await client.get("/v1/weather/state") }
}
