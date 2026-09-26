import Foundation

extension DeviceSettings {
    /// The per-app colours that may be `null`: null means "inherit
    /// `textColor`". A server older than the NG 1.1 alignment rejects null.
    public static let inheritableColorKeys: Set<String> = [
        "timeColor", "dateColor", "temperatureColor", "humidityColor", "batteryColor",
    ]

    /// The keys that changed between `old` and `self`, as a PUT body. The clock
    /// merges a settings PATCH key by key, so sending only what changed keeps
    /// a save from rewriting values it never touched (the Pomodoro takeover
    /// flips `autoTransition`/`blockNavigation` behind the app's back). A
    /// per-app colour set back to nil is sent as an explicit `null`. A nested
    /// object (`scroll`, `weekdayBar`) is sent whole when any field changed.
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
    /// inherit colours). A 0.27.x server filters its GET to its whitelist, so
    /// the sound keys are simply missing there.
    public var serverSupportsNG11: Bool {
        soundEnabled != nil || buzzerVolume != nil
    }
}
