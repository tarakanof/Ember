import Foundation

/// Fires an Apple Reminder's alarm on the clock via POST /v1/reminders/fire.
public struct RemindersService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    /// Fires one reminder occurrence. `key` (see `reminderDedupeKey`) lets the
    /// server ignore a retry of an occurrence it already pushed to the clock.
    public func fire(text: String, sound: Bool, duration: Int, nativeIconId: String, hold: Bool,
                     repeatSound: Bool = false, key: String) async throws {
        try await client.postIdempotent("/v1/reminders/fire",
                                        body: ReminderFireBody(text: text, sound: sound, duration: duration,
                                                               nativeIconId: nativeIconId, hold: hold,
                                                               repeatSound: repeatSound),
                                        key: key)
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
