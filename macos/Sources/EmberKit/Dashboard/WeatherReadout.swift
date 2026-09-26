import Foundation

/// The Weather card's derived values.
public enum WeatherReadout {
    /// European AQI bands (EEA): the chip's word and colour key.
    public enum AirLevel: Int, Comparable, Sendable, CaseIterable {
        case good, fair, moderate, poor, veryPoor, extremelyPoor

        public init(europeanAQI aqi: Double) {
            switch aqi {
            case ..<20: self = .good
            case ..<40: self = .fair
            case ..<60: self = .moderate
            case ..<80: self = .poor
            case ..<100: self = .veryPoor
            default: self = .extremelyPoor
            }
        }

        public static func < (a: AirLevel, b: AirLevel) -> Bool { a.rawValue < b.rawValue }
    }

    /// The temperature in the display unit the server was configured with
    /// ("imperial" → °F, anything else °C).
    public static func temperature(celsius: Double, units: String) -> Measurement<UnitTemperature> {
        let c = Measurement(value: celsius, unit: UnitTemperature.celsius)
        return units == "imperial" ? c.converted(to: .fahrenheit) : c
    }

    /// The next `hours` hourly points from `now`'s hour on, for the card's
    /// small temperature line; empty when fewer than two remain.
    public static func upcomingHours(_ points: [WeatherState.TempPoint], from now: Date,
                                     hours: Int = 12) -> [WeatherState.TempPoint] {
        let start = now.addingTimeInterval(-3600)
        let next = points.filter { $0.time > start }.sorted { $0.time < $1.time }.prefix(hours)
        return next.count >= 2 ? Array(next) : []
    }
}
