import Foundation

/// What the knob reports in its checkins (`knobSettings.diagnostics`).
public enum KnobDiagnostics: String, Codable, CaseIterable, Sendable, Identifiable {
    /// Nothing beyond the checkin's own fields.
    case off
    /// CPU, memory, temperature, Wi-Fi, reset reason.
    case basic
    /// Basic plus request and rendering stats.
    case full

    public var id: Self { self }

    public init(from decoder: Decoder) throws {
        self = KnobDiagnostics(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .off
    }
}

/// The dashboard's time ranges (`GET /v1/devices/{id}/stats?range=`).
public enum KnobStatsRange: String, CaseIterable, Sendable, Identifiable {
    case fifteenMinutes = "15m"
    case hour = "1h"
    case day = "24h"

    public var id: Self { self }

    public var duration: TimeInterval {
        switch self {
        case .fifteenMinutes: 15 * 60
        case .hour: 3600
        case .day: 86400
        }
    }

    /// How often the dashboard asks while this range is shown.
    public var pollInterval: Duration {
        switch self {
        case .fifteenMinutes: .seconds(5)
        case .hour: .seconds(15)
        case .day: .seconds(60)
        }
    }

    /// Spacing of the server's points when the knob reports normally.
    public var spacing: TimeInterval {
        switch self {
        case .fifteenMinutes, .hour: 60
        case .day: 300
        }
    }
}

/// `GET /v1/devices/{id}/stats` (`knobStatsView` in cmd/ember/devices_stats.go).
public struct KnobStats: Codable, Equatable, Sendable {
    /// One report, or a bucket of them; nil where the knob didn't report it.
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
        }

        public init(t: Date) { self.t = t }

        /// The mean across cores, nil without a CPU reading.
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
    public var latest: Sample?
    public var points: [Sample]

    enum CodingKeys: String, CodingKey {
        case diagnostics, range, online, latest, points
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

    /// The knob reported PSRAM (boards without it leave it null).
    public var hasPSRAM: Bool { points.contains { $0.psramFreeBytes != nil } || latest?.psramFreeBytes != nil }

    /// The knob reports request and rendering stats.
    public var hasFullStats: Bool {
        points.contains { $0.requestsPerMin != nil || $0.renderFPS != nil }
            || latest?.requestsPerMin != nil || latest?.renderFPS != nil
    }

    /// Live mode is on at `now`.
    public func isLive(now: Date) -> Bool { liveUntil.map { $0 > now } ?? false }
}

/// A point of one named line; `segment` changes across a reporting gap so
/// the chart doesn't draw a line through time the knob was silent.
public struct KnobSeriesPoint: Equatable, Sendable, Identifiable {
    public var t: Date
    public var series: String
    public var segment: Int
    public var value: Double
    public var id: String { "\(series)|\(t.timeIntervalSinceReferenceDate)" }
    /// The key that keeps segments of one series apart.
    public var lineKey: String { "\(series)#\(segment)" }
}

extension KnobStats {
    /// Gap after which a line breaks: three missed reports at the range's
    /// normal spacing.
    public static func gap(for range: KnobStatsRange) -> TimeInterval { range.spacing * 3 }

    /// One series per named value, broken at reporting gaps.
    public func series(_ values: [(name: String, value: (Sample) -> Double?)],
                       range: KnobStatsRange) -> [KnobSeriesPoint] {
        var out: [KnobSeriesPoint] = []
        let gap = Self.gap(for: range)
        for (name, value) in values {
            var segment = 0
            var previous: Date?
            for p in points {
                guard let v = value(p) else { continue }
                if let prev = previous, p.t.timeIntervalSince(prev) > gap { segment += 1 }
                previous = p.t
                out.append(KnobSeriesPoint(t: p.t, series: name, segment: segment, value: v))
            }
        }
        return out
    }

    /// Per-core CPU lines named by `name(core)`.
    public func cpuSeries(range: KnobStatsRange, name: (Int) -> String) -> [KnobSeriesPoint] {
        let cores = points.map { $0.cpuPercent?.count ?? 0 }.max() ?? 0
        return series((0..<cores).map { core in
            (name(core), { s in s.cpuPercent.flatMap { $0.indices.contains(core) ? $0[core] : nil } })
        }, range: range)
    }
}

/// Thresholds the dashboard flags.
public enum KnobReadout {
    /// Above this chip temperature (°C) the reading is a warning.
    public static let hotC = 70.0
    /// Above this average CPU (%) the reading is a warning.
    public static let busyCPU = 85.0
    /// Below this largest free internal block (bytes), pages may fail to draw.
    public static let lowLargestBlock = 24 * 1024
    /// Frame rate the knob aims for.
    public static let targetFPS = 30.0
}
