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
        public var todayMinC: Double?
        public var todayMaxC: Double?
        public var cloudPct: Double?
        public var uv: Double?
        public var hourly: [TempPoint]

        enum CodingKeys: String, CodingKey {
            case stale, condition, severe, hourly, uv
            case fetchedAt = "fetched_at"
            case conditionCode = "condition_code"
            case tempC = "temp_c"
            case todayMinC = "today_min_c"
            case todayMaxC = "today_max_c"
            case cloudPct = "cloud_pct"
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
        public var night: Bool?
    }

    public struct Faces: Decodable, Sendable, Equatable {
        public struct Spell: Decodable, Sendable, Equatable {
            public var start: Date
            public var end: Date
            public var intensity: Int
            public var snow: Bool
        }

        public struct Rain: Decodable, Sendable, Equatable {
            public var until: Date?
            public var spells: [Spell]
        }

        public struct Day: Decodable, Sendable, Equatable {
            public var start: Date
            public var end: Date
            public var condition: String?
            public var conditionCode: String?
            public var minC: Int?
            public var maxC: Int?
            public var popPct: Int?

            enum CodingKeys: String, CodingKey {
                case start, end, condition
                case conditionCode = "condition_code"
                case minC = "min_c"
                case maxC = "max_c"
                case popPct = "pop_pct"
            }
        }

        public struct Twilight: Decodable, Sendable, Equatable {
            public var dawn, blueEnd, goldenEnd, goldenStart, blueStart, dusk: Date?

            enum CodingKeys: String, CodingKey {
                case dawn, dusk
                case blueEnd = "blue_end"
                case goldenEnd = "golden_end"
                case goldenStart = "golden_start"
                case blueStart = "blue_start"
            }
        }

        public struct Moon: Decodable, Sendable, Equatable {
            public var phasePct: Int
            public var illumPct: Int
            public var up: Bool?

            enum CodingKeys: String, CodingKey {
                case up
                case phasePct = "phase_pct"
                case illumPct = "illum_pct"
            }
        }

        public var utcOffsetS: Int
        public var night: Bool
        public var windKmh: Int?
        public var gustKmh: Int?
        public var windDirDeg: Int?
        public var rainIntensity: Int?
        public var todayPopPct: Int?
        public var rain: Rain?
        public var days: [Day]
        public var twilight: Twilight?
        public var moon: Moon?
        public var aqi: Int?

        enum CodingKeys: String, CodingKey {
            case night, rain, days, twilight, moon, aqi
            case utcOffsetS = "utc_offset_s"
            case windKmh = "wind_kmh"
            case gustKmh = "gust_kmh"
            case windDirDeg = "wind_dir_deg"
            case rainIntensity = "rain_intensity"
            case todayPopPct = "today_pop_pct"
        }
    }

    public var generatedAt: Date
    public var enabled: Bool
    public var provider: String
    public var units: String
    public var locationName: String?
    public var current: Current?
    public var air: Air?
    public var sun: Sun?
    public var faces: Faces?

    enum CodingKeys: String, CodingKey {
        case enabled, provider, units, current, air, sun, faces
        case generatedAt = "generated_at"
        case locationName = "location_name"
    }
}
