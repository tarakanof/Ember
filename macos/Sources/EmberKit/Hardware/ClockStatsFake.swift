import Foundation

public enum ClockStatsFake {
    public static func make(range: HardwareRange, now: Date = Date(timeIntervalSinceReferenceDate: 812_000_000),
                            online: Bool = true, gap: ClosedRange<TimeInterval>? = nil) -> ClockStats {
        var points: [ClockStats.Sample] = []
        var t = now.addingTimeInterval(-range.duration)
        let slow = range == .day ? 24.0 : 1
        while t <= now {
            let back = now.timeIntervalSince(t)
            let silent = (gap.map { $0.contains(back) } ?? false) || (!online && back <= 25 * 60)
            points.append(silent ? unreachable(at: t) : sample(at: t, slow: slow))
            let step: TimeInterval = range == .fifteenMinutes && back <= 600 ? 30 : range.spacing
            t = t.addingTimeInterval(step)
        }
        let latest = points.last { $0.reachable }
        return ClockStats(range: range, reachable: online, checkedAt: points.last?.t, ipAddress: "192.0.2.66",
                          latest: latest, points: points)
    }

    static func unreachable(at t: Date) -> ClockStats.Sample {
        ClockStats.Sample(t: t, reachable: false)
    }

    static func sample(at t: Date, slow: Double = 1) -> ClockStats.Sample {
        let x = t.timeIntervalSinceReferenceDate
        func wave(_ p: Double, _ phase: Double = 0) -> Double { sin(x / (p * slow) + phase) }
        var s = ClockStats.Sample(t: t)
        s.rssiDBm = Int((-66 + 5 * wave(800) + 2 * wave(91, 1)).rounded())
        s.freeHeapBytes = 101_000 + Int(4_000 * wave(600)) - Int(2_000 * max(0, wave(83, 2)))
        s.minFreeHeapBytes = 76_544
        s.temperatureC = ((31.5 + 1.5 * wave(2_400)) * 10).rounded() / 10
        s.humidityPercent = ((38 + 4 * wave(3_000, 1)) * 10).rounded() / 10
        let lux = max(0, 60 + 70 * wave(1_500, 0.3))
        s.lightLux = (lux * 10).rounded() / 10
        s.batteryPercent = (96 - 4 * max(0, wave(5_000))).rounded()
        s.publishOK = Int((3 + 2 * wave(170)).rounded())
        s.publishFail = wave(1_900, 1) > 0.95 ? 1 : 0
        return s
    }
}
