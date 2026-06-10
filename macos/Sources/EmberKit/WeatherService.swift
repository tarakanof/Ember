import Foundation

/// Typed wrapper over GET/PUT /v1/weather/config.
public struct WeatherService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func getConfig() async throws -> WeatherConfig { try await client.get("/v1/weather/config") }
    public func putConfig(_ cfg: WeatherConfig) async throws { try await client.put("/v1/weather/config", body: cfg) }
}

/// Typed wrapper over GET/PUT /v1/usage/config (the AI-usage-widget toggles).
public struct UsageService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func getConfig() async throws -> UsageConfig { try await client.get("/v1/usage/config") }
    public func putConfig(_ cfg: UsageConfig) async throws { try await client.put("/v1/usage/config", body: cfg) }
}

/// Fires an Apple Reminder's alarm on the clock via POST /v1/reminders/fire.
public struct RemindersService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func fire(text: String, sound: Bool, duration: Int, nativeIconId: String, hold: Bool) async throws {
        try await client.post("/v1/reminders/fire",
                              body: ReminderFireBody(text: text, sound: sound, duration: duration,
                                                     nativeIconId: nativeIconId, hold: hold))
    }
}

struct ReminderFireBody: Encodable {
    var text: String
    var sound: Bool
    var duration: Int
    var nativeIconId: String
    var hold: Bool
    enum CodingKeys: String, CodingKey {
        case text, sound, duration, hold
        case nativeIconId = "native_icon_id"
    }
}
