import Foundation

public struct ClockStats: Codable, Equatable, Sendable {
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
    public var configured: Bool
    public var reachable: Bool?
    public var checkedAt: Date?
    public var ipAddress: String?
    public var sampleIntervalSec: Int
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

    public var online: Bool { reachable == true }

    public func series(_ values: [(name: String, value: (Sample) -> Double?)],
                       range: HardwareRange) -> [HardwareSeriesPoint] {
        HardwareSeries.build(points, time: \.t, values: values, gap: range.gap)
    }

    public func has(_ value: (Sample) -> Double?) -> Bool {
        points.contains { value($0) != nil } || latest.map { value($0) != nil } ?? false
    }
}

public enum ClockReadout {
    public static let lowHeap = 30_000
    public static let fairHeap = 50_000
    public static let hotC = 40.0
    public static let lowBattery = 20.0

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
