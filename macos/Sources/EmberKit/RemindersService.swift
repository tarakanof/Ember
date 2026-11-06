import Foundation

public struct RemindersService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func fire(text: String, sound: Bool, duration: Int, nativeIconId: String, hold: Bool,
                     repeatSound: Bool = false, key: String) async throws {
        try await client.postIdempotent("/v1/reminders/fire",
                                        body: ReminderFireBody(text: text, sound: sound, duration: duration,
                                                               nativeIconId: nativeIconId, hold: hold,
                                                               repeatSound: repeatSound),
                                        key: key)
    }

    public func saveConfigIfDefault(_ prefs: ReminderPrefs) async throws -> ReminderPrefs? {
        do {
            return try await client.request("PUT", "/v1/reminders/config",
                                            query: [URLQueryItem(name: "if_default", value: "1")], body: prefs)
        } catch APIError.http(status: 409, body: _) {
            return nil
        }
    }
}

struct ReminderFireBody: Encodable {
    var text: String
    var sound: Bool
    var duration: Int
    var nativeIconId: String
    var hold: Bool
    var repeatSound: Bool
    enum CodingKeys: String, CodingKey {
        case text, sound, duration, hold
        case nativeIconId = "native_icon_id"
        case repeatSound = "repeat_sound"
    }
}
