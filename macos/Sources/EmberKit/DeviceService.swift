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
    /// Fallback for servers that predate /v1/device/screen: read the clock's
    /// /api/screen directly (read-only, same LAN — what the AWTRIX app does).
    public static func directScreen(clockBaseURL: String) async throws -> [Int] {
        let base = clockBaseURL.hasSuffix("/") ? String(clockBaseURL.dropLast()) : clockBaseURL
        guard let url = URL(string: base + "/api/screen") else { throw URLError(.badURL) }
        var req = URLRequest(url: url)
        req.timeoutInterval = 5
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        return try JSONDecoder().decode([Int].self, from: data)
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
