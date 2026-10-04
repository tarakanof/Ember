import Foundation

/// `GET /v1/clock/stats` (`clockStatsView` in cmd/ember/clock_stats.go): the
/// server's probes of the clock, every 30 s.
public struct ClockStats: Codable, Equatable, Sendable {
    /// One probe, or a bucket of them; readings are nil where the clock
    /// didn't answer or has no such sensor.
    public struct Sample: Codable, Equatable, Sendable, Identifiable {
        public var t: Date
        public var reachable: Bool
        public var rssiDBm: Int?
        public var freeHeapBytes: Int?
        public var minFreeHeapBytes: Int?
        public var temperatureC: Double?
        public var humidityPercent: Double?
        public var lightLux: Double?
        public var batteryPercent: Double?
        /// Publishes to the clock since the previous sample.
        public var publishOK: Int
        public var publishFail: Int

        public var id: Date { t }

        enum CodingKeys: String, CodingKey {
            case t, reachable
            case rssiDBm = "rssi_dbm"
            case freeHeapBytes = "free_heap_bytes"
            case minFreeHeapBytes = "min_free_heap_bytes"
            case temperatureC = "temperature_c"
            case humidityPercent = "humidity_percent"
            case lightLux = "light_lux"
            case batteryPercent = "battery_percent"
            case publishOK = "publish_ok"
            case publishFail = "publish_fail"
        }

        public init(t: Date, reachable: Bool = true) {
            self.t = t; self.reachable = reachable; publishOK = 0; publishFail = 0
        }
    }

    public var range: String
    /// The server has a clock address.
    public var configured: Bool
    /// The newest probe got an answer; nil before the first probe.
    public var reachable: Bool?
    public var checkedAt: Date?
    public var ipAddress: String?
    public var sampleIntervalSec: Int
    /// The newest sample that reached the clock.
    public var latest: Sample?
    public var points: [Sample]

    enum CodingKeys: String, CodingKey {
        case range, configured, reachable, latest, points
        case checkedAt = "checked_at"
        case ipAddress = "ip_address"
        case sampleIntervalSec = "sample_interval_sec"
    }

    public init(range: HardwareRange, configured: Bool = true, reachable: Bool?, checkedAt: Date?,
                ipAddress: String? = nil, sampleIntervalSec: Int = 30, latest: Sample?, points: [Sample]) {
        self.range = range.rawValue; self.configured = configured; self.reachable = reachable
        self.checkedAt = checkedAt; self.ipAddress = ipAddress; self.sampleIntervalSec = sampleIntervalSec
        self.latest = latest; self.points = points
    }

    /// The clock answered the newest probe.
    public var online: Bool { reachable == true }

    /// One series per named value, broken at gaps (unreachable probes
    /// carry no readings, so they break the line too).
    public func series(_ values: [(name: String, value: (Sample) -> Double?)],
                       range: HardwareRange) -> [HardwareSeriesPoint] {
        HardwareSeries.build(points, time: \.t, values: values, gap: range.gap)
    }

    /// The clock reported this reading at all in the range.
    public func has(_ value: (Sample) -> Double?) -> Bool {
        points.contains { value($0) != nil } || latest.map { value($0) != nil } ?? false
    }
}

/// Thresholds the Clock › Hardware page flags.
public enum ClockReadout {
    /// Below this free heap (bytes) the clock may fail to draw or answer.
    public static let lowHeap = 30_000
    /// Below this free heap (bytes) the reading is only fair.
    public static let fairHeap = 50_000
    /// Above this temperature (°C, the clock's sensor inside its case) it reads hot.
    public static let hotC = 40.0
    /// Below this battery level (%) it reads low when the clock doesn't say.
    public static let lowBattery = 20.0

    /// The share of `ok + fail` publishes delivered, never rounded up to
    /// 100 % while any failed ("99.6 %"); nil without publishes.
    public static func delivered(ok: Int, fail: Int, locale: Locale = .current) -> String? {
        let n = ok + fail
        guard n > 0 else { return nil }
        let ratio = Double(ok) / Double(n)
        if fail > 0, ratio >= 0.995 {
            let floored = (ratio * 1000).rounded(.down) / 1000
            return floored.formatted(.percent.precision(.fractionLength(1)).locale(locale))
        }
        return Percent.text(ratio: ratio, locale: locale)
    }
}
