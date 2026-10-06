import Foundation

public struct DeviceService: Sendable, Equatable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public static func == (a: DeviceService, b: DeviceService) -> Bool {
        a.client.baseURL == b.client.baseURL && a.client.token == b.client.token
    }

    public func settings() async throws -> DeviceSettings {
        try await client.get("/v1/device/settings", budget: .clockLong)
    }
    public func update(_ patch: DeviceSettings) async throws {
        try await client.put("/v1/device/settings", body: patch, budget: .clockLong)
    }
    public func update(patch: [String: JSONValue]) async throws {
        try await client.put("/v1/device/settings", body: JSONValue.object(patch), budget: .clockLong)
    }
    public func display() async throws -> DeviceDisplay {
        try await client.get("/v1/device/display", budget: .clock)
    }
    public func updateDisplay(_ patch: DeviceDisplay) async throws {
        try await client.put("/v1/device/display", body: patch, budget: .clock)
    }
    public func setDisplayPower(_ on: Bool) async throws {
        try await client.put("/v1/device/display/power", body: DisplayPowerUpdate(power: on), budget: .clock)
    }
    public func playTestChime(melody: String? = nil) async throws {
        if let melody {
            try await client.post("/v1/device/audio/test", body: AudioTestRequest(melody: melody), budget: .clock)
        } else {
            try await client.send("POST", "/v1/device/audio/test", budget: .clock)
        }
    }
    public func stopAudio() async throws {
        try await client.send("POST", "/v1/device/audio/stop", budget: .clock)
    }
    public func melodies() async throws -> DeviceMelodyList {
        try await client.get("/v1/device/audio/melodies", budget: .clock)
    }
    public func apps() async throws -> [AppInfo] {
        try await client.get("/v1/device/apps", budget: .clock)
    }
    public func updateApps(_ patch: AppsUpdate) async throws {
        try await client.put("/v1/device/apps", body: patch, budget: .clock)
    }
    public func capabilities() async throws -> DeviceCapabilities {
        try await client.get("/v1/device/capabilities", budget: .clock)
    }
    public func stats() async throws -> DeviceStats {
        try await client.get("/v1/device/stats", budget: .clock)
    }
    public func sensors() async throws -> SensorCalibration {
        try await client.get("/v1/device/sensors", budget: .clock)
    }
    public func updateSensors(_ cal: SensorCalibration) async throws {
        try await client.put("/v1/device/sensors", body: cal, budget: .clockLong)
    }
    public func screen() async throws -> [Int] {
        let frame: ScreenFrame = try await client.get("/v1/device/screen", budget: .clock)
        return frame.pixels
    }
    private static let directScreenSession = URLSession(configuration: .ephemeral)
    public static func directScreen(clockBaseURL: String) async throws -> [Int] {
        let base = clockBaseURL.hasSuffix("/") ? String(clockBaseURL.dropLast()) : clockBaseURL
        guard let url = URL(string: base + "/api/v1/display/screen") else { throw URLError(.badURL) }
        var req = URLRequest(url: url)
        req.timeoutInterval = 5
        let (data, resp) = try await directScreenSession.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        return try JSONDecoder().decode(ScreenFrame.self, from: data).pixels
    }
    public func reboot() async throws {
        try await client.send("POST", "/v1/device/reboot", budget: .clock)
    }
    public func dismiss() async throws {
        try await client.send("POST", "/v1/device/notify/dismiss", budget: .clock)
    }
    public func nextApp() async throws {
        try await client.send("POST", "/v1/device/app/next", budget: .clock)
    }
    public func previousApp() async throws {
        try await client.send("POST", "/v1/device/app/previous", budget: .clock)
    }

    public func config() async throws -> DeviceConfig {
        try await client.get("/v1/device/config")
    }
    public func setConfig(baseURL: String) async throws {
        try await client.put("/v1/device/config", body: ["base_url": baseURL])
    }
    public func discover() async throws -> DiscoverResult {
        try await client.get("/v1/device/discover", budget: .clock)
    }
    public func buttons() async throws -> ButtonStatus {
        try await client.get("/v1/device/buttons", budget: .clock)
    }
    public func updateButtons(enabled: Bool) async throws -> ButtonStatus {
        try await client.put("/v1/device/buttons", body: ButtonsUpdate(enabled: enabled), budget: .clockLong)
        return try await buttons()
    }
}
