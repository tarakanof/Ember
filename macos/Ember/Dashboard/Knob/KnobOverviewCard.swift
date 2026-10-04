import SwiftUI
import EmberKit

/// The knob's newest readings: four gauges and the facts that don't chart.
/// Offline, it says how old they are and is drawn muted.
struct KnobOverviewCard: View {
    let stats: KnobStats
    var firmware: String?
    var now = Date()

    @Environment(\.accessibilityDifferentiateWithoutColor) private var differentiateWithoutColor

    var body: some View {
        let l = stats.latest
        DashboardCard(title: "Now", systemImage: "gauge.with.dots.needle.33percent", height: 196) {
            HStack(alignment: .center, spacing: 20) {
                HStack(alignment: .top, spacing: 0) {
                    gauge("Processor", value: l?.cpuAverage, in: 0...100, text: l?.cpuAverage.map(KnobFormat.percent),
                          tint: Self.level(l?.cpuAverage, good: { $0 < 70 }, fair: { $0 < KnobReadout.busyCPU }),
                          warn: (l?.cpuAverage ?? 0) > KnobReadout.busyCPU)
                    gauge("Temperature", value: l?.tempC, in: 20...85, text: l?.tempC.map(KnobFormat.celsius),
                          tint: Self.level(l?.tempC, good: { $0 < 60 }, fair: { $0 < KnobReadout.hotC }),
                          warn: (l?.tempC ?? 0) > KnobReadout.hotC)
                    gauge("Wi-Fi", value: l?.rssiDBm.map(Double.init), in: -90 ... -30,
                          text: l?.rssiDBm.map { KnobFormat.dbm(Double($0)) },
                          tint: Self.level(l?.rssiDBm.map(Double.init), good: { $0 >= -67 }, fair: { $0 >= Double(ClockHealthReadout.weakRSSI) }),
                          warn: l?.rssiDBm.map { ClockHealthReadout.wifi(rssi: $0).weak } ?? false)
                    if stats.diagnostics == .full {
                        gauge("Frame rate", value: l?.renderFPS, in: 0...KnobReadout.targetFPS,
                              text: l?.renderFPS.map(KnobFormat.fps),
                              tint: Self.level(l?.renderFPS, good: { $0 >= 24 }, fair: { $0 >= 15 }), warn: false)
                    }
                }
                .frame(maxWidth: .infinity)
                Divider()
                facts(l).frame(maxWidth: .infinity, alignment: .leading)
            }
            .saturation(stats.online ? 1 : 0)
            .opacity(stats.online ? 1 : 0.6)
        } accessory: {
            if !stats.online, let at = l?.t ?? stats.lastSeen {
                Text("As of \(Text(at, format: .dateTime.hour().minute()))",
                     comment: "Dashboard knob Now card while offline: the time of the readings shown (\"As of 21:43\").")
            }
        }
    }

    private func gauge(_ title: LocalizedStringKey, value: Double?, in range: ClosedRange<Double>, text: String?,
                       tint: Color, warn: Bool) -> some View {
        VStack(spacing: 8) {
            Gauge(value: min(max(value ?? range.lowerBound, range.lowerBound), range.upperBound), in: range) {
                Text(title)
            } currentValueLabel: {
                Text(verbatim: text ?? "—")
            }
            .gaugeStyle(KnobRingGaugeStyle(tint: tint, diameter: 76))
            HStack(spacing: 3) {
                if warn {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .imageScale(.small)
                }
                Text(title)
            }
            .lineLimit(1)
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
        .accessibilityValue([text ?? String(localized: "Not available"),
                             warn ? String(localized: "Needs attention") : nil].compactMap { $0 }.joined(separator: ", "))
    }

    private func facts(_ l: KnobStats.Sample?) -> some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 12, verticalSpacing: 8) {
            GridRow {
                fact("Free memory", value: l?.heapInternalFreeBytes.map { KnobFormat.bytes(Double($0)) }, gap: true)
                fact("Largest block", value: l?.heapInternalLargestBytes.map { KnobFormat.bytes(Double($0)) },
                     warn: (l?.heapInternalLargestBytes ?? .max) < KnobReadout.lowLargestBlock)
            }
            GridRow {
                fact("PSRAM free", value: l?.psramFreeBytes.map { KnobFormat.bytes(Double($0)) }, gap: true)
                fact("Uptime", value: l?.uptimeSec.map { DurationText.uptime($0) })
            }
            GridRow {
                fact("Last restart", value: stats.resetReason.map(Self.resetReason), gap: true)
                fact("Firmware", value: firmware)
            }
        }
        .font(.callout)
    }

    @ViewBuilder
    private func fact(_ title: LocalizedStringKey, value: String?, warn: Bool = false, gap: Bool = false) -> some View {
        Text(title).foregroundStyle(.secondary)
        HStack(alignment: .firstTextBaseline, spacing: 3) {
            if warn {
                Image(systemName: "exclamationmark.triangle.fill").imageScale(.small).foregroundStyle(.orange)
            }
            Text(verbatim: value ?? "—")
                .monospacedDigit()
                .lineLimit(1)
                .foregroundStyle(warn ? AnyShapeStyle(.orange) : AnyShapeStyle(.primary))
        }
        .padding(.trailing, gap ? 16 : 0)
        .accessibilityLabel(Text(title))
        .accessibilityValue(Text(verbatim: [value ?? String(localized: "Not available"),
                                            warn ? String(localized: "Needs attention") : nil].compactMap { $0 }
                                     .joined(separator: ", ")))
    }

    /// The arc colour for a reading: green when normal, yellow when fair,
    /// orange past that; a fuller arc always means more of the quantity.
    static func level(_ v: Double?, good: (Double) -> Bool, fair: (Double) -> Bool) -> Color {
        guard let v else { return .secondary }
        if good(v) { return .green }
        return fair(v) ? .yellow : .orange
    }

    /// The ESP-IDF reset reason the knob reported, in words.
    static func resetReason(_ raw: String) -> String {
        switch raw {
        case "poweron": String(localized: "Power on")
        case "sw": String(localized: "Restarted by software")
        case "panic": String(localized: "Crash")
        case "int_wdt", "task_wdt", "wdt": String(localized: "Watchdog")
        case "brownout": String(localized: "Low voltage")
        case "deepsleep": String(localized: "Woke from sleep")
        case "ext": String(localized: "Reset pin")
        default: raw
        }
    }
}

/// A 270° ring gauge at a real size (the system's accessory style is fixed
/// at watch-complication size): track, tinted value arc, value in the middle.
struct KnobRingGaugeStyle: GaugeStyle {
    let tint: Color
    var diameter: CGFloat = 76

    func makeBody(configuration: Configuration) -> some View {
        let line: CGFloat = 7
        let span = 0.75
        ZStack {
            Circle()
                .trim(from: 0, to: span)
                .stroke(.quaternary, style: StrokeStyle(lineWidth: line, lineCap: .round))
                .rotationEffect(.degrees(135))
            Circle()
                .trim(from: 0, to: span * configuration.value)
                .stroke(tint, style: StrokeStyle(lineWidth: line, lineCap: .round))
                .rotationEffect(.degrees(135))
            configuration.currentValueLabel
                .font(.system(.callout, design: .rounded).weight(.semibold))
                .monospacedDigit()
                .minimumScaleFactor(0.6)
                .lineLimit(1)
                .padding(.horizontal, line + 4)
        }
        .frame(width: diameter, height: diameter)
    }
}
