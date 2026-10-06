import Foundation

public struct WeatherState: Decodable, Sendable, Equatable {
    public struct TempPoint: Decodable, Sendable, Equatable, Identifiable {
        public var time: Date
        public var tempC: Double
        public var id: Date { time }

        enum CodingKeys: String, CodingKey {
            case time
            case tempC = "temp_c"
        }
    }

    public struct AQIPoint: Decodable, Sendable, Equatable, Identifiable {
        public var time: Date
        public var europeanAqi: Double
        public var id: Date { time }

        enum CodingKeys: String, CodingKey {
            case time
            case europeanAqi = "european_aqi"
        }
    }

    public struct Current: Decodable, Sendable, Equatable {
        public var fetchedAt: Date
        public var stale: Bool
        public var condition: String
        public var conditionCode: String?
        public var severe: Bool
        public var tempC: Double
        public var hourly: [TempPoint]

        enum CodingKeys: String, CodingKey {
            case stale, condition, severe, hourly
            case fetchedAt = "fetched_at"
            case conditionCode = "condition_code"
            case tempC = "temp_c"
        }
    }

    public struct Air: Decodable, Sendable, Equatable {
        public var fetchedAt: Date
        public var stale: Bool
        public var europeanAqi: Double
        public var pm25Ugm3: Double
        public var pm10Ugm3: Double
        public var hourly: [AQIPoint]

        enum CodingKeys: String, CodingKey {
            case stale, hourly
            case fetchedAt = "fetched_at"
            case europeanAqi = "european_aqi"
            case pm25Ugm3 = "pm2_5_ugm3"
            case pm10Ugm3 = "pm10_ugm3"
        }
    }

    public struct Sun: Decodable, Sendable, Equatable {
        public var sunrise: Date
        public var sunset: Date
    }

    public var generatedAt: Date
    public var enabled: Bool
    public var provider: String
    public var units: String
    public var locationName: String?
    public var current: Current?
    public var air: Air?
    public var sun: Sun?

    enum CodingKeys: String, CodingKey {
        case enabled, provider, units, current, air, sun
        case generatedAt = "generated_at"
        case locationName = "location_name"
    }
}
