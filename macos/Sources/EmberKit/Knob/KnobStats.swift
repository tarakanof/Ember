import Foundation

public enum KnobDiagnostics: String, Codable, CaseIterable, Sendable, Identifiable {
    case off
    case basic
    case full

    public var id: Self { self }

    public init(from decoder: Decoder) throws {
        self = KnobDiagnostics(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .off
    }
}

public typealias KnobStatsRange = HardwareRange

extension HardwareRange {
    public var pollInterval: Duration {
        switch self {
        case .fifteenMinutes: .seconds(5)
        case .hour: .seconds(15)
        case .day: .seconds(60)
        }
    }
}

public struct KnobStats: Codable, Equatable, Sendable {
    public struct Sample: Codable, Equatable, Sendable, Identifiable {
        public var t: Date
        public var uptimeSec: Int?
        public var rssiDBm: Int?
        public var cpuPercent: [Double]?
        public var heapInternalFreeBytes: Int?
        public var heapInternalMinBytes: Int?
        public var heapInternalLargestBytes: Int?
        public var psramFreeBytes: Int?
        public var psramMinBytes: Int?
        public var psramLargestBytes: Int?
        public var tempC: Double?
        public var requestsPerMin: Double?
        public var requestFailuresPerMin: Double?
        public var requestLatencyAvgMS: Double?
        public var requestLatencyMaxMS: Int?
        public var renderFPS: Double?
        public var frameAvgMS: Double?
        public var frameMaxMS: Int?
        public var brightnessLevel: Int?

        public var id: Date { t }

        enum CodingKeys: String, CodingKey {
            case t
            case uptimeSec = "uptime_sec"
            case rssiDBm = "rssi_dbm"
            case cpuPercent = "cpu_percent"
            case heapInternalFreeBytes = "heap_internal_free_bytes"
            case heapInternalMinBytes = "heap_internal_min_bytes"
            case heapInternalLargestBytes = "heap_internal_largest_bytes"
            case psramFreeBytes = "psram_free_bytes"
            case psramMinBytes = "psram_min_bytes"
            case psramLargestBytes = "psram_largest_bytes"
            case tempC = "temp_c"
            case requestsPerMin = "requests_per_min"
            case requestFailuresPerMin = "request_failures_per_min"
            case requestLatencyAvgMS = "request_latency_avg_ms"
            case requestLatencyMaxMS = "request_latency_max_ms"
            case renderFPS = "render_fps"
            case frameAvgMS = "frame_avg_ms"
            case frameMaxMS = "frame_max_ms"
            case brightnessLevel = "brightness_level"
        }

        public init(t: Date) { self.t = t }

        public var brightnessPercent: Double? { brightnessLevel.map { Double($0) / 255 * 100 } }

        public var cpuAverage: Double? {
            guard let c = cpuPercent, !c.isEmpty else { return nil }
            return c.reduce(0, +) / Double(c.count)
        }
    }

    public var deviceID: String
    public var diagnostics: KnobDiagnostics
    public var range: String
    public var online: Bool
    public var lastSeen: Date?
    public var liveUntil: Date?
    public var resetReason: String?
    public var statsIntervalS: Int?
    public var latest: Sample?
    public var points: [Sample]

    enum CodingKeys: String, CodingKey {
        case diagnostics, range, online, latest, points
        case statsIntervalS = "stats_interval_s"
        case deviceID = "device_id"
        case lastSeen = "last_seen"
        case liveUntil = "live_until"
        case resetReason = "reset_reason"
    }

    public init(deviceID: String, diagnostics: KnobDiagnostics, range: KnobStatsRange, online: Bool,
                lastSeen: Date?, liveUntil: Date? = nil, resetReason: String? = nil,
                latest: Sample?, points: [Sample]) {
        self.deviceID = deviceID; self.diagnostics = diagnostics; self.range = range.rawValue
        self.online = online; self.lastSeen = lastSeen; self.liveUntil = liveUntil
        self.resetReason = resetReason; self.latest = latest; self.points = points
    }

    public var hasPSRAM: Bool { points.contains { $0.psramFreeBytes != nil } || latest?.psramFreeBytes != nil }

    public var hasFullStats: Bool {
        points.contains { $0.requestsPerMin != nil || $0.renderFPS != nil }
            || latest?.requestsPerMin != nil || latest?.renderFPS != nil
    }

    public func isLive(now: Date) -> Bool { liveUntil.map { $0 > now } ?? false }
}

extension KnobStats {
    public func gap(for range: KnobStatsRange) -> TimeInterval {
        max(range.gap, TimeInterval(3 * (statsIntervalS ?? 0)))
    }

    public func series(_ values: [(name: String, value: (Sample) -> Double?)],
                       range: KnobStatsRange) -> [HardwareSeriesPoint] {
        HardwareSeries.build(points, time: \.t, values: values, gap: gap(for: range))
    }

    public func cpuSeries(range: KnobStatsRange, name: (Int) -> String) -> [HardwareSeriesPoint] {
        let cores = points.map { $0.cpuPercent?.count ?? 0 }.max() ?? 0
        return series((0..<cores).map { core in
            (name(core), { s in s.cpuPercent.flatMap { $0.indices.contains(core) ? $0[core] : nil } })
        }, range: range)
    }
}

public enum KnobReadout {
    public static let hotC = 70.0
    public static let busyCPU = 85.0
    public static let lowLargestBlock = 24 * 1024
    public static let targetFPS = 30.0
}
