import Foundation

public enum ClockHealthReadout {
    public static let weakRSSI = -80

    public static let disabledTitle: LocalizedStringResource = "Clock disabled on this server"

    public static func wifi(rssi: Int) -> (strength: Double, weak: Bool) {
        let s = min(1, max(0, Double(rssi + 90) / 40))
        return (s, rssi < weakRSSI)
    }

    public static func batterySymbol(percent: Double) -> String {
        switch percent {
        case ..<13: "battery.0"
        case ..<38: "battery.25"
        case ..<63: "battery.50"
        case ..<88: "battery.75"
        default: "battery.100"
        }
    }

    public static func publishRatio(_ p: ClockHealth.Publish) -> Double? {
        guard p.ok24h + p.fail24h > 0 else { return nil }
        return p.successRatio24h ?? Double(p.ok24h) / Double(p.ok24h + p.fail24h)
    }

    public static func publishIsPoor(_ ratio: Double) -> Bool { ratio < 0.95 }
}
