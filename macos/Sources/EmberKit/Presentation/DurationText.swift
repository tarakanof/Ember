import Foundation

/// Durations as the UI shows them. All go through `Duration.formatted`, never
/// `String(format:)`, so they follow the locale.
public enum DurationText {
    /// A countdown: "18:42", or "1:02:05" from an hour up. Negative clamps to 0.
    public static func remaining(_ seconds: Int, locale: Locale = .current) -> String {
        let s = max(0, seconds)
        let pattern: Duration.TimeFormatStyle.Pattern = s >= 3600 ? .hourMinuteSecond : .minuteSecond
        return Duration.seconds(s).formatted(Duration.TimeFormatStyle(pattern: pattern).locale(locale))
    }

    /// A total in minutes: "1h 15m", "45m", "0m".
    public static func minutes(_ minutes: Int, locale: Locale = .current) -> String {
        Duration.seconds(max(0, minutes) * 60).formatted(
            Duration.UnitsFormatStyle(allowedUnits: [.hours, .minutes], width: .narrow).locale(locale))
    }

    /// An uptime: the two largest units, "6h 40m", "3d 4h", "12m".
    public static func uptime(_ seconds: Int, locale: Locale = .current) -> String {
        Duration.seconds(max(0, seconds)).formatted(
            Duration.UnitsFormatStyle(allowedUnits: [.days, .hours, .minutes], width: .narrow,
                                      maximumUnitCount: 2).locale(locale))
    }
}

/// Percentages as the UI shows them.
public enum Percent {
    /// 47 → "47%". Takes a 0...100 value, as the server sends.
    public static func text(_ percent: Double, locale: Locale = .current) -> String {
        (percent / 100).formatted(.percent.precision(.fractionLength(0)).locale(locale))
    }

    /// A 0...1 ratio → "82%".
    public static func text(ratio: Double, locale: Locale = .current) -> String {
        ratio.formatted(.percent.precision(.fractionLength(0)).locale(locale))
    }
}
