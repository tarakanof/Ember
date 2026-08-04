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
/// display/apps/capabilities, stats, actions, and discovery/config).
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
    /// NG's ambient-weather overlay (GET/PUT /v1/device/display).
    public func display() async throws -> DeviceDisplay {
        try await client.get("/v1/device/display")
    }
    public func updateDisplay(_ patch: DeviceDisplay) async throws {
        try await client.put("/v1/device/display", body: patch)
    }
    /// The clock's native apps (Time, Date, Temperature, Humidity, Battery, and
    /// any pushed/scripted app) with their enabled/inLoop state.
    public func apps() async throws -> [AppInfo] {
        try await client.get("/v1/device/apps")
    }
    /// Replaces AWTRIX3's TIM/DAT/TEMP/HUM/BAT toggles: sets the native-app
    /// display order and which ones are disabled.
    public func updateApps(_ patch: AppsUpdate) async throws {
        try await client.put("/v1/device/apps", body: patch)
    }
    /// The device's live effect/transition/overlay/palette catalogue, used to
    /// feed pickers instead of a hardcoded table. Pinned server contract; a 404
    /// from an older server (or one still bringing this endpoint up) should be
    /// handled by the caller falling back to DeviceKnownValues.fallbackTransitions.
    public func capabilities() async throws -> DeviceCapabilities {
        try await client.get("/v1/device/capabilities")
    }
    public func stats() async throws -> DeviceStats {
        try await client.get("/v1/device/stats")
    }
    /// The calibration offsets on the clock's system object (nil = firmware
    /// default). Applies live — no reboot.
    public func sensors() async throws -> SensorCalibration {
        try await client.get("/v1/device/sensors")
    }
    /// Writes the offsets into the clock's system object. Applies live — no
    /// reboot follows.
    public func updateSensors(_ cal: SensorCalibration) async throws {
        try await client.put("/v1/device/sensors", body: cal)
    }
    /// The clock's live framebuffer: 24-bit RGB ints, row-major. NG wraps the
    /// pixel array in {"width","height","pixels"}; this unwraps it.
    public func screen() async throws -> [Int] {
        let frame: ScreenFrame = try await client.get("/v1/device/screen")
        return frame.pixels
    }
    /// Fallback for servers that predate /v1/device/screen: read the clock's
    /// awtrix-ng display/screen endpoint directly (read-only, same LAN — what
    /// the AWTRIX app does), unwrapping the same envelope as the proxy.
    public static func directScreen(clockBaseURL: String) async throws -> [Int] {
        let base = clockBaseURL.hasSuffix("/") ? String(clockBaseURL.dropLast()) : clockBaseURL
        guard let url = URL(string: base + "/api/v1/display/screen") else { throw URLError(.badURL) }
        var req = URLRequest(url: url)
        req.timeoutInterval = 5
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        return try JSONDecoder().decode(ScreenFrame.self, from: data).pixels
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
    /// Sets (enabled:true) or clears (enabled:false) the clock's buttonCallback
    /// so it points at this server — one click instead of hand-editing the
    /// clock's system config. Re-fetches the status so the caller sees the
    /// clock's confirmed state.
    public func updateButtons(enabled: Bool) async throws -> ButtonStatus {
        try await client.put("/v1/device/buttons", body: ButtonsUpdate(enabled: enabled))
        return try await buttons()
    }
}
