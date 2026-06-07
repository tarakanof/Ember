import Foundation

/// Typed wrapper over GET/PUT /v1/weather/config.
public struct WeatherService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func getConfig() async throws -> WeatherConfig { try await client.get("/v1/weather/config") }
    public func putConfig(_ cfg: WeatherConfig) async throws { try await client.put("/v1/weather/config", body: cfg) }
}

/// Fires an Apple Reminder's alarm on the clock via POST /v1/reminders/fire.
public struct RemindersService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func fire(text: String, sound: Bool, duration: Int, nativeIconId: String) async throws {
        try await client.post("/v1/reminders/fire",
                              body: ReminderFireBody(text: text, sound: sound, duration: duration, nativeIconId: nativeIconId))
    }
}

struct ReminderFireBody: Encodable {
    var text: String
    var sound: Bool
    var duration: Int
    var nativeIconId: String
    enum CodingKeys: String, CodingKey {
        case text, sound, duration
        case nativeIconId = "native_icon_id"
    }
}
