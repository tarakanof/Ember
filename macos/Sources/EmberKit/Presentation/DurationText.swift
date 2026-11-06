import Foundation

public enum DurationText {
    public static func remaining(_ seconds: Int, locale: Locale = .current) -> String {
        let s = max(0, seconds)
        let pattern: Duration.TimeFormatStyle.Pattern = s >= 3600 ? .hourMinuteSecond : .minuteSecond
        return Duration.seconds(s).formatted(Duration.TimeFormatStyle(pattern: pattern).locale(locale))
    }

    public static func minutes(_ minutes: Int, locale: Locale = .current) -> String {
        Duration.seconds(max(0, minutes) * 60).formatted(
            Duration.UnitsFormatStyle(allowedUnits: [.hours, .minutes], width: .narrow).locale(locale))
    }

    public static func interval(_ seconds: Int, locale: Locale = .current) -> String {
        Duration.seconds(max(0, seconds)).formatted(
            Duration.UnitsFormatStyle(allowedUnits: [.minutes, .seconds], width: .abbreviated).locale(locale))
    }

    public static func uptime(_ seconds: Int, locale: Locale = .current) -> String {
        Duration.seconds(max(0, seconds)).formatted(
            Duration.UnitsFormatStyle(allowedUnits: [.days, .hours, .minutes], width: .narrow,
                                      maximumUnitCount: 2).locale(locale))
    }
}

public enum Percent {
    public static func text(_ percent: Double, locale: Locale = .current) -> String {
        (percent / 100).formatted(.percent.precision(.fractionLength(0)).locale(locale))
    }

    public static func text(ratio: Double, locale: Locale = .current) -> String {
        ratio.formatted(.percent.precision(.fractionLength(0)).locale(locale))
    }
}
