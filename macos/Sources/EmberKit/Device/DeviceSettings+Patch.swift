import Foundation

extension DeviceSettings {
    public static let inheritableColorKeys: Set<String> = [
        "timeColor", "dateColor", "temperatureColor", "humidityColor", "batteryColor",
    ]

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

    public var serverSupportsNG11: Bool {
        soundEnabled != nil || buzzerVolume != nil
    }
}
