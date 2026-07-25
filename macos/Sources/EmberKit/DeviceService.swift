import Foundation

/// Why a `/v1/device/*` call failed, split by *who* is unreachable — the server
/// or the clock behind it.
///
/// The distinction decides what the UI can offer as a fix. Only a 502 means the
/// server answered and its proxy to the clock did not, which is exactly the case
/// clock discovery repairs. `notConfigured` and `transport` mean the *app* can't
/// reach the server: scanning for a clock would find one and then fail to save
/// it, so those must point at the Connection settings instead. A timeout proves
/// nothing about either end, so it claims nothing.
///
/// The 502 contract is the server's, pinned there by
/// `TestDeviceProxyMapsDeviceErrorTo502` (cmd/ember/device_settings_test.go):
/// the `/v1/device/*` handlers map every clock-side failure — unreachable,
/// non-200, unparseable, or no clock configured at all — to 502. Observing it
/// means out-waiting the server's 8s clock budget; see `APIClient.forDeviceProxy`.
public enum DeviceFailure: Equatable, Sendable {
    case unauthorized
    case serverUnreachable
    case clockUnreachable
    case timedOut
    case other

    /// Classifies a thrown error; anything that isn't an `APIError` is `.other`.
    public static func classify(_ error: Error) -> DeviceFailure {
        guard let api = error as? APIError else { return .other }
        switch api {
        case .notConfigured, .transport: return .serverUnreachable
        case .timeout: return .timedOut
        case .http(401, _): return .unauthorized
        case .http(502, _): return .clockUnreachable
        case .http, .decoding: return .other
        }
    }
}

/// Typed wrapper over the server's /v1/device/* proxy endpoints (clock settings,
/// stats, actions, and discovery/config).
public struct DeviceService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    /// False when no server URL is configured, i.e. every call here would fail
    /// with `.notConfigured`. The UI checks this before offering clock discovery:
    /// a scan would still find the clock, and then have nowhere to save it.
    public var isConfigured: Bool { client.baseURL != nil }

    public func settings() async throws -> DeviceSettings {
        try await client.get("/v1/device/settings")
    }
    public func update(_ patch: DeviceSettings) async throws {
        try await client.put("/v1/device/settings", body: patch)
    }
    public func stats() async throws -> DeviceStats {
        try await client.get("/v1/device/stats")
    }
    /// The calibration offsets in the clock's dev.json (nil = firmware default).
    public func sensors() async throws -> SensorCalibration {
        try await client.get("/v1/device/sensors")
    }
    /// Writes the offsets into dev.json and reboots the clock (offsets only
    /// apply at boot).
    public func updateSensors(_ cal: SensorCalibration) async throws {
        try await client.put("/v1/device/sensors", body: cal)
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
    /// Advance the clock to the next app in its rotation (AWTRIX /api/nextapp).
    public func nextApp() async throws {
        try await client.send("POST", "/v1/device/app/next")
    }
    /// Step the clock back to the previous app (AWTRIX /api/previousapp).
    public func previousApp() async throws {
        try await client.send("POST", "/v1/device/app/previous")
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
