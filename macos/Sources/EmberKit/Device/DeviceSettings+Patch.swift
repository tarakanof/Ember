import Foundation

extension DeviceSettings {
    /// The per-app colours that may be `null`: null means "inherit
    /// `textColor`".
    public static let inheritableColorKeys: Set<String> = [
        "timeColor", "dateColor", "temperatureColor", "humidityColor", "batteryColor",
    ]

    /// The keys that changed between `old` and `self`, as a PUT body.
    public func patch(from old: DeviceSettings) -> [String: JSONValue] {
        let new = (try? JSONValue.object(encoding: self)) ?? [:]
        let before = (try? JSONValue.object(encoding: old)) ?? [:]
        var out: [String: JSONValue] = [:]
        for key in Set(new.keys).union(before.keys) {
            let n = new[key], o = before[key]
            guard n != o else { continue }
            if let n {
                out[key] = n
            } else if Self.inheritableColorKeys.contains(key) {
                out[key] = .null
            }
        }
        return out
    }

    /// Whether the server writes the NG 1.1 keys (mute, buzzer volume,
    /// inherit colours).
    public var serverSupportsNG11: Bool {
        soundEnabled != nil || buzzerVolume != nil
    }
}
