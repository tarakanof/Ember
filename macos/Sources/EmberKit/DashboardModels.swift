import Foundation

// Wire models for the dashboard read endpoints (cmd/ember/dashboard_http.go and
// GET /v1/pomodoro/workhours). The server emits RFC 3339 timestamps with whole
// seconds, null for "no value", series as arrays of points, and the unit in
// every key, so these decode with APIClient's .iso8601 decoder and feed Swift
// Charts directly.

/// GET /v1/usage — the latest subscription-usage snapshot per tool.
public struct UsageSnapshot: Decodable, Sendable, Equatable {
    /// One quota window. `resetsAt` is nil when the producer didn't report it.
    public struct Window: Decodable, Sendable, Equatable {
        public var usedPercent: Double
        public var resetsAt: Date?
        public var resetLabel: String?

        enum CodingKeys: String, CodingKey {
            case usedPercent = "used_percent"
            case resetsAt = "resets_at"
            case resetLabel = "reset_label"
        }
    }

    /// A per-model window (e.g. "opus"); the server sorts them by name.
    public struct ModelWindow: Decodable, Sendable, Equatable, Identifiable {
        public var model: String
        public var usedPercent: Double
        public var resetsAt: Date?
        public var resetLabel: String?
        public var id: String { model }

        enum CodingKeys: String, CodingKey {
            case model
            case usedPercent = "used_percent"
            case resetsAt = "resets_at"
            case resetLabel = "reset_label"
        }
    }

    public struct Tool: Decodable, Sendable, Equatable, Identifiable {
        public var tool: String
        public var source: String?
        public var updatedAt: Date
        /// Older than `staleAfterSec`; the clock stops showing it too.
        public var stale: Bool
        public var fiveHour: Window?
        public var sevenDay: Window?
        public var models: [ModelWindow]
        public var id: String { tool }

        enum CodingKeys: String, CodingKey {
            case tool, source, stale, models
            case updatedAt = "updated_at"
            case fiveHour = "five_hour"
            case sevenDay = "seven_day"
        }
    }

    public var generatedAt: Date
    public var staleAfterSec: Int
    /// Sorted by tool name.
    public var tools: [Tool]

    enum CodingKeys: String, CodingKey {
        case tools
        case generatedAt = "generated_at"
        case staleAfterSec = "stale_after_sec"
    }
}

/// GET /v1/activity/summary — agent activity per tool and per source.
public struct ActivitySummary: Decodable, Sendable, Equatable {
    /// One rollup row. `key` is the tool or source name; nil on a window total.
    public struct Totals: Decodable, Sendable, Equatable, Identifiable {
        public var key: String?
        /// Wall-clock active time; concurrent sessions of one group count once.
        public var activeSec: Int
        public var sessions: Int
        /// Waiting episodes: how often an agent stopped to ask for input.
        public var attention: Int
        public var id: String { key ?? "" }

        enum CodingKeys: String, CodingKey {
            case key, sessions, attention
            case activeSec = "active_sec"
        }
    }

    public struct Window: Decodable, Sendable, Equatable {
        public var from: Date
        public var to: Date
        public var total: Totals
        /// Most active first.
        public var byTool: [Totals]
        /// Most active first.
        public var bySource: [Totals]

        enum CodingKeys: String, CodingKey {
            case from, to, total
            case byTool = "by_tool"
            case bySource = "by_source"
        }
    }

    /// One (day, tool) bar. The series is zero-filled and oldest first, so a
    /// stacked `BarMark(x: .value("Day", $0.date, unit: .day), y: …)` works as is.
    public struct DailyPoint: Decodable, Sendable, Equatable, Identifiable {
        /// Logical day ("2026-09-26"), honouring the server's day-start hour.
        public var day: String
        /// Local midnight of `day`.
        public var date: Date
        public var tool: String
        public var activeSec: Int
        public var id: String { "\(day)|\(tool)" }

        enum CodingKeys: String, CodingKey {
            case day, date, tool
            case activeSec = "active_sec"
        }
    }

    public var generatedAt: Date
    /// False while the server's work-hours activity overlay is off: nothing new
    /// is being stored, so recent windows read as zero.
    public var recording: Bool
    public var days: Int
    public var spanGapSec: Int
    public var today: Window
    /// The last `days` logical days, today included.
    public var period: Window
    public var daily: [DailyPoint]

    enum CodingKeys: String, CodingKey {
        case recording, days, today, period, daily
        case generatedAt = "generated_at"
        case spanGapSec = "span_gap_sec"
    }
}

/// GET /v1/weather/state — the server's cached weather observation.
/// Temperatures are always Celsius; `units` is the display preference.
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
        /// Render bucket: clear, clouds, fog, rain, snow or storm.
        public var condition: String
        public var severe: Bool
        public var tempC: Double
        public var hourly: [TempPoint]

        enum CodingKeys: String, CodingKey {
            case stale, condition, severe, hourly
            case fetchedAt = "fetched_at"
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
    /// nil until the first successful fetch.
    public var current: Current?
    /// nil until the first air-quality fetch.
    public var air: Air?
    /// nil without a location, or during polar day/night.
    public var sun: Sun?

    enum CodingKeys: String, CodingKey {
        case enabled, provider, units, current, air, sun
        case generatedAt = "generated_at"
    }
}

/// GET /v1/clock/health — server→clock publish record plus the clock's own
/// telemetry (cached server-side for 30 s).
public struct ClockHealth: Decodable, Sendable, Equatable {
    public struct Publish: Decodable, Sendable, Equatable {
        /// Server start; the counters reset on restart.
        public var countingSince: Date
        public var okTotal: Int
        public var failTotal: Int
        /// Lost first attempts that a retry recovered.
        public var retriesTotal: Int
        /// ok / (ok + fail), 0...1; nil before the first publish.
        public var successRatio: Double?
        public var lastAt: Date?
        public var lastOk: Bool

        enum CodingKeys: String, CodingKey {
            case countingSince = "counting_since"
            case okTotal = "ok_total"
            case failTotal = "fail_total"
            case retriesTotal = "retries_total"
            case successRatio = "success_ratio"
            case lastAt = "last_at"
            case lastOk = "last_ok"
        }
    }

    /// Telemetry fields are nil when the clock is unreachable or its firmware
    /// doesn't report them.
    public struct Device: Decodable, Sendable, Equatable {
        public var reachable: Bool
        public var checkedAt: Date
        public var firmware: String?
        public var uptimeSec: Int?
        public var freeHeapBytes: Int?
        public var minFreeHeapBytes: Int?
        public var wifiRssiDbm: Int?
        /// Wi-Fi (re)connects since boot; above 1 means the link dropped.
        public var wifiConnects: Int?
        public var resetReason: String?
        public var fps: Double?
        public var batteryPercent: Double?
        public var temperatureC: Double?
        public var humidityPercent: Double?

        enum CodingKeys: String, CodingKey {
            case reachable, firmware, fps
            case checkedAt = "checked_at"
            case uptimeSec = "uptime_sec"
            case freeHeapBytes = "free_heap_bytes"
            case minFreeHeapBytes = "min_free_heap_bytes"
            case wifiRssiDbm = "wifi_rssi_dbm"
            case wifiConnects = "wifi_connects"
            case resetReason = "reset_reason"
            case batteryPercent = "battery_percent"
            case temperatureC = "temperature_c"
            case humidityPercent = "humidity_percent"
        }
    }

    public var generatedAt: Date
    public var publish: Publish
    /// nil when the server has no clock configured.
    public var device: Device?
    public var lastButtonAt: Date?

    enum CodingKeys: String, CodingKey {
        case publish, device
        case generatedAt = "generated_at"
        case lastButtonAt = "last_button_at"
    }
}

/// GET /v1/pomodoro/workhours — sessionized work span per logical day.
public struct WorkHours: Decodable, Sendable, Equatable {
    public struct Day: Decodable, Sendable, Equatable, Identifiable {
        /// Logical day ("2026-09-26").
        public var date: String
        /// nil on a day with no work.
        public var workStart: Date?
        public var workEnd: Date?
        public var spanSec: Int
        public var activeSec: Int
        public var breakSec: Int
        public var sessions: Int
        public var longestSec: Int
        public var id: String { date }

        enum CodingKeys: String, CodingKey {
            case date, sessions
            case workStart = "work_start"
            case workEnd = "work_end"
            case spanSec = "span_sec"
            case activeSec = "active_sec"
            case breakSec = "break_sec"
            case longestSec = "longest_sec"
        }
    }

    /// Most recent first.
    public var days: [Day]
    public var gapMin: Int
    /// Agent activity is unioned with focus blocks.
    public var includeActivity: Bool

    enum CodingKeys: String, CodingKey {
        case days
        case gapMin = "gap_min"
        case includeActivity = "include_activity"
    }
}
