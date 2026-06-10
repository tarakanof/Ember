import Foundation

/// Typed wrapper over the server's /v1/device/* proxy endpoints (clock settings,
/// stats, actions, and discovery/config).
public struct DeviceService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func settings() async throws -> DeviceSettings {
        try await client.get("/v1/device/settings")
    }
    public func update(_ patch: DeviceSettings) async throws {
        try await client.put("/v1/device/settings", body: patch)
    }
    public func stats() async throws -> DeviceStats {
        try await client.get("/v1/device/stats")
    }
    /// The clock's live framebuffer: 24-bit RGB ints, row-major 32×8.
    public func screen() async throws -> [Int] {
        try await client.get("/v1/device/screen")
    }
    public func reboot() async throws {
        try await client.send("POST", "/v1/device/reboot")
    }
    public func dismiss() async throws {
        try await client.send("POST", "/v1/device/notify/dismiss")
    }

    public func config() async throws -> DeviceConfig {
        try await client.get("/v1/device/config")
    }
    public func setConfig(baseURL: String) async throws {
        try await client.put("/v1/device/config", body: ["base_url": baseURL])
    }
    public func discover() async throws -> DiscoverResult {
        try await client.get("/v1/device/discover")
    }
    public func buttons() async throws -> ButtonStatus {
        try await client.get("/v1/device/buttons")
    }
}
