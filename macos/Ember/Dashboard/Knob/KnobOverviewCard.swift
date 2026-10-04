import SwiftUI
import EmberKit

/// The knob's current readings: four gauges and the facts that don't chart.
struct KnobOverviewCard: View {
    let stats: KnobStats
    var firmware: String?
    var now = Date()

    @Environment(\.accessibilityDifferentiateWithoutColor) private var differentiateWithoutColor

    var body: some View {
        let l = stats.latest
        DashboardCard(title: "Now", systemImage: "gauge.with.dots.needle.33percent", height: 176) {
            HStack(alignment: .top, spacing: 24) {
                HStack(alignment: .top, spacing: 18) {
                    gauge("Processor", value: l?.cpuAverage, in: 0...100, text: l?.cpuAverage.map(KnobFormat.percent),
                          tint: Gradient(colors: [.green, .yellow, .orange]),
                          warn: (l?.cpuAverage ?? 0) > KnobReadout.busyCPU)
                    gauge("Temperature", value: l?.tempC, in: 20...85, text: l?.tempC.map { "\(Int($0.rounded()))°" },
                          tint: Gradient(colors: [.teal, .yellow, .orange, .red]),
                          warn: (l?.tempC ?? 0) > KnobReadout.hotC, accessibilityText: l?.tempC.map(KnobFormat.celsius))
                    gauge("Wi-Fi (dBm)", value: l?.rssiDBm.map(Double.init), in: -90 ... -30,
                          text: l?.rssiDBm.map { "\($0)" },
                          tint: Gradient(colors: [.red, .orange, .yellow, .green]),
                          warn: l?.rssiDBm.map { ClockHealthReadout.wifi(rssi: $0).weak } ?? false,
                          accessibilityText: l?.rssiDBm.map { KnobFormat.dbm(Double($0)) })
                    if stats.diagnostics == .full {
                        gauge("Frame rate", value: l?.renderFPS, in: 0...KnobReadout.targetFPS,
                              text: l?.renderFPS.map { $0.formatted(.number.precision(.fractionLength(0))) },
                              tint: Gradient(colors: [.red, .orange, .green]), warn: false,
                              accessibilityText: l?.renderFPS.map(KnobFormat.fps))
                    }
                }
                Divider()
                facts(l)
            }
        }
    }

    private func gauge(_ title: LocalizedStringKey, value: Double?, in range: ClosedRange<Double>, text: String?,
                       tint: Gradient, warn: Bool, accessibilityText: String? = nil) -> some View {
        VStack(spacing: 6) {
            Gauge(value: min(max(value ?? range.lowerBound, range.lowerBound), range.upperBound), in: range) {
                EmptyView()
            } currentValueLabel: {
                Text(verbatim: text ?? "—").monospacedDigit()
            }
            .gaugeStyle(.accessoryCircular)
            .tint(tint)
            .controlSize(.large)
            .scaleEffect(1.15)
            .frame(width: 64, height: 64)
            HStack(spacing: 3) {
                if warn {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .imageScale(.small)
                }
                Text(title)
            }
            .lineLimit(1)
            .fixedSize()
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
        .accessibilityValue([accessibilityText ?? text ?? String(localized: "Not available"),
                             warn ? String(localized: "Needs attention") : nil].compactMap { $0 }.joined(separator: ", "))
    }

    private func facts(_ l: KnobStats.Sample?) -> some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 12, verticalSpacing: 6) {
            fact("Free memory", value: l?.heapInternalFreeBytes.map { KnobFormat.bytes(Double($0)) },
                 note: l?.heapInternalLargestBytes.map {
                     String(localized: "largest block \(KnobFormat.bytes(Double($0)))",
                            comment: "Dashboard knob: the largest free memory block (\"largest block 31 KB\").")
                 },
                 warn: (l?.heapInternalLargestBytes ?? .max) < KnobReadout.lowLargestBlock)
            if let free = l?.psramFreeBytes {
                fact("PSRAM free", value: KnobFormat.bytes(Double(free)))
            }
            fact("Uptime", value: l?.uptimeSec.map { DurationText.uptime($0) })
            fact("Last restart", value: stats.resetReason.map(Self.resetReason))
            fact("Firmware", value: firmware)
            GridRow {
                Text("Last report").foregroundStyle(.secondary)
                if let seen = stats.lastSeen {
                    Text(seen, format: .relative(presentation: .named))
                } else {
                    Text(verbatim: "—")
                }
            }
        }
        .font(.callout)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func fact(_ title: LocalizedStringKey, value: String?, note: String? = nil, warn: Bool = false) -> some View {
        GridRow {
            Text(title).foregroundStyle(.secondary)
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                if warn && differentiateWithoutColor {
                    Image(systemName: "exclamationmark.triangle.fill").imageScale(.small)
                }
                Text(verbatim: value ?? "—")
                    .monospacedDigit()
                    .foregroundStyle(warn ? AnyShapeStyle(.orange) : AnyShapeStyle(.primary))
                if let note { Text(verbatim: note).foregroundStyle(.secondary).lineLimit(1) }
            }
        }
        .accessibilityElement(children: .combine)
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
