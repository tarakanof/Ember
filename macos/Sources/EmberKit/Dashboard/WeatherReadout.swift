import Foundation

public enum WeatherReadout {
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

    public static func temperature(celsius: Double, units: String) -> Measurement<UnitTemperature> {
        let c = Measurement(value: celsius, unit: UnitTemperature.celsius)
        return units == "imperial" ? c.converted(to: .fahrenheit) : c
    }

    public static func upcomingHours(_ points: [WeatherState.TempPoint], from now: Date,
                                     hours: Int = 12) -> [WeatherState.TempPoint] {
        let start = now.addingTimeInterval(-3600)
        let next = points.filter { $0.time > start }.sorted { $0.time < $1.time }.prefix(hours)
        return next.count >= 2 ? Array(next) : []
    }
}
