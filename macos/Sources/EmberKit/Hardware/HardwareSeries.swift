import Foundation

public enum HardwareRange: String, CaseIterable, Sendable, Identifiable {
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

    public var spacing: TimeInterval {
        switch self {
        case .fifteenMinutes, .hour: 60
        case .day: 300
        }
    }

    public var gap: TimeInterval { spacing * 3 }
}

public struct HardwareSeriesPoint: Equatable, Sendable, Identifiable {
    public var t: Date
    public var series: String
    public var segment: Int
    public var value: Double
    public var id: String { "\(series)|\(t.timeIntervalSinceReferenceDate)" }
    public var lineKey: String { "\(series)#\(segment)" }

    public init(t: Date, series: String, segment: Int, value: Double) {
        self.t = t; self.series = series; self.segment = segment; self.value = value
    }
}

public enum HardwareSeries {
    public static func build<P>(_ points: [P], time: (P) -> Date,
                                values: [(name: String, value: (P) -> Double?)],
                                gap: TimeInterval) -> [HardwareSeriesPoint] {
        var out: [HardwareSeriesPoint] = []
        for (name, value) in values {
            var segment = 0
            var previous: Date?
            for p in points {
                guard let v = value(p) else { continue }
                let t = time(p)
                if let prev = previous, t.timeIntervalSince(prev) > gap { segment += 1 }
                previous = t
                out.append(HardwareSeriesPoint(t: t, series: name, segment: segment, value: v))
            }
        }
        return out
    }
}

extension HardwareSeries {
    public static func summed(_ points: [HardwareSeriesPoint], per unit: Calendar.Component,
                              calendar: Calendar = .current) -> [HardwareSeriesPoint] {
        var order: [String] = []
        var sums: [String: HardwareSeriesPoint] = [:]
        for p in points {
            let start = calendar.dateInterval(of: unit, for: p.t)?.start ?? p.t
            let key = "\(p.series)|\(start.timeIntervalSinceReferenceDate)"
            if var have = sums[key] {
                have.value += p.value
                sums[key] = have
            } else {
                order.append(key)
                sums[key] = HardwareSeriesPoint(t: start, series: p.series, segment: 0, value: p.value)
            }
        }
        return order.compactMap { sums[$0] }
    }
}

public enum WiFiReadout {
    public static let goodRSSI = -67
    public static var weakRSSI: Int { ClockHealthReadout.weakRSSI }
}
