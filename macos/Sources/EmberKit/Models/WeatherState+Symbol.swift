import Foundation

extension WeatherState {
    public var sfSymbol: String { sfSymbol(at: Date()) }

    public func sfSymbol(at now: Date) -> String {
        guard let current else { return "questionmark.circle" }
        let night = sun.map { now < $0.sunrise || now >= $0.sunset } ?? false
        switch current.condition {
        case "clear": return night ? "moon.stars.fill" : "sun.max.fill"
        case "clouds": return night ? "cloud.moon.fill" : "cloud.sun.fill"
        case "fog": return "cloud.fog.fill"
        case "rain": return "cloud.rain.fill"
        case "snow": return "cloud.snow.fill"
        case "storm": return "cloud.bolt.rain.fill"
        default: return "cloud.fill"
        }
    }
}
