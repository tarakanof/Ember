import Foundation

public enum KnobStatsFake {
    public static func make(range: KnobStatsRange, diagnostics: KnobDiagnostics = .full,
                            now: Date = Date(timeIntervalSinceReferenceDate: 812_000_000),
                            online: Bool = true, live: Bool = true, psram: Bool = true,
                            gap: ClosedRange<TimeInterval>? = nil) -> KnobStats {
        guard diagnostics != .off else {
            return KnobStats(deviceID: "knob-61fc8c", diagnostics: .off, range: range, online: online,
                             lastSeen: now.addingTimeInterval(-20), latest: nil, points: [])
        }
        let end = online ? now : now.addingTimeInterval(-25 * 60)
        var times: [Date] = []
        var t = now.addingTimeInterval(-range.duration)
        while t <= end {
            let back = now.timeIntervalSince(t)
            if gap.map({ !$0.contains(back) }) ?? true { times.append(t) }
            let step: TimeInterval = range == .fifteenMinutes && live && back <= 600 ? 5 : range.spacing
            t = t.addingTimeInterval(step)
        }
        let slow = range == .day ? 24.0 : 1
        let points = times.map { sample(at: $0, diagnostics: diagnostics, psram: psram, slow: slow) }
        return KnobStats(deviceID: "knob-61fc8c", diagnostics: diagnostics, range: range, online: online,
                         lastSeen: times.last, liveUntil: live && online ? now.addingTimeInterval(150) : nil,
                         resetReason: "poweron", latest: points.last, points: points)
    }

    static func sample(at t: Date, diagnostics: KnobDiagnostics, psram: Bool, slow: Double = 1) -> KnobStats.Sample {
        let x = t.timeIntervalSinceReferenceDate
        func wave(_ p: Double, _ phase: Double = 0) -> Double { sin(x / (p * slow) + phase) }
        var s = KnobStats.Sample(t: t)
        s.uptimeSec = Int(x.truncatingRemainder(dividingBy: 400_000))
        s.rssiDBm = Int((-62 + 6 * wave(900) + 2 * wave(97, 1)).rounded())
        let busy = max(0, wave(420, 2)) * 30
        s.cpuPercent = [min(100, 18 + busy + 6 * wave(61)), min(100, 34 + busy * 0.6 + 9 * wave(37, 1))]
            .map { ($0 * 10).rounded() / 10 }
        let free = 48_000 + Int(6_000 * wave(700))
        s.heapInternalFreeBytes = free
        s.heapInternalLargestBytes = free - 14_000 + Int(2_000 * wave(130))
        s.heapInternalMinBytes = 29_500
        if psram {
            s.psramFreeBytes = 6_900_000 + Int(250_000 * wave(1_100, 0.5))
            s.psramLargestBytes = (s.psramFreeBytes ?? 0) - 400_000
            s.psramMinBytes = 6_450_000
        }
        s.tempC = ((43 + 4 * wave(1_800) + busy / 10) * 10).rounded() / 10
        s.brightnessLevel = Int(min(255, max(10, 150 + 100 * wave(1_500, 0.3))))
        guard diagnostics == .full else { return s }
        s.requestsPerMin = (31 + 4 * wave(300)).rounded()
        s.requestFailuresPerMin = wave(2_400, 1) > 0.93 ? 2 : 0
        s.requestLatencyAvgMS = ((38 + 9 * wave(500, 3)) * 10).rounded() / 10
        s.requestLatencyMaxMS = Int(110 + 50 * max(0, wave(200)))
        s.renderFPS = ((29.2 - busy / 15 + 0.4 * wave(53)) * 10).rounded() / 10
        s.frameAvgMS = ((12 + busy / 6) * 10).rounded() / 10
        s.frameMaxMS = Int(28 + busy / 2 + 6 * max(0, wave(71)))
        return s
    }
}
