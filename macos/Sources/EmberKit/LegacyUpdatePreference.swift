import Foundation

public enum LegacyUpdatePreference {
    public static let enabledKey = "updates.checkAutomatically"
    static let retiredKeys = [enabledKey, "updates.lastAttempt", "updates.lastRelease"]

    public static func take(from defaults: UserDefaults) -> Bool? {
        let enabled = defaults.object(forKey: enabledKey) as? Bool
        for key in retiredKeys { defaults.removeObject(forKey: key) }
        return enabled
    }
}
