import Foundation

/// Thresholds and symbols for the Clock health card, so the view only lays
/// out what this decides.
public enum ClockHealthReadout {
    /// Below this the Wi-Fi reading is shown as a warning.
    public static let weakRSSI = -80

    /// Signal bars for an RSSI: `wifi` strength variable value 0...1 and
    /// whether it's weak.
    public static func wifi(rssi: Int) -> (strength: Double, weak: Bool) {
        // -90 dBm and under is no signal, -50 and over full.
        let s = min(1, max(0, Double(rssi + 90) / 40))
        return (s, rssi < weakRSSI)
    }

    /// `battery.0` … `battery.100` for a 0...100 level.
    public static func batterySymbol(percent: Double) -> String {
        switch percent {
        case ..<13: "battery.0"
        case ..<38: "battery.25"
        case ..<63: "battery.50"
        case ..<88: "battery.75"
        default: "battery.100"
        }
    }

    /// The publish success rate to show: nil without publishes in 24 h.
    public static func publishRatio(_ p: ClockHealth.Publish) -> Double? {
        guard p.ok24h + p.fail24h > 0 else { return nil }
        return p.successRatio24h ?? Double(p.ok24h) / Double(p.ok24h + p.fail24h)
    }

    /// Worth a warning colour: under 95 % of publishes landed.
    public static func publishIsPoor(_ ratio: Double) -> Bool { ratio < 0.95 }
}
