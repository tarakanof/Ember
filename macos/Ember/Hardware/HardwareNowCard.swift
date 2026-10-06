import SwiftUI
import EmberKit

struct HardwareGauge: Identifiable {
    let id: String
    let title: LocalizedStringKey
    var value: Double?
    var range: ClosedRange<Double>
    var text: String?
    var tint: Color
    var warn = false
}

struct HardwareFact: Identifiable {
    let id: String
    let title: LocalizedStringKey
    var value: String?
    var note: String?
    var warn = false
}

struct HardwareNowCard: View {
    let gauges: [HardwareGauge]
    let facts: [HardwareFact]
    let online: Bool
    var asOf: Date?

    private var factRows: [[HardwareFact]] {
        stride(from: 0, to: facts.count, by: 2).map { Array(facts[$0..<min($0 + 2, facts.count)]) }
    }

    var body: some View {
        let rows = factRows
        DashboardCard(title: "Now", systemImage: "gauge.with.dots.needle.33percent",
                      height: 148 + CGFloat(rows.count) * 25) {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .top, spacing: 0) {
                    ForEach(gauges) { gauge($0) }
                }
                .frame(maxWidth: .infinity)
                Divider()
                Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 12, verticalSpacing: 8) {
                    ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                        GridRow {
                            ForEach(Array(row.enumerated()), id: \.element.id) { i, f in
                                fact(f, gap: i == 0)
                            }
                        }
                    }
                }
                .font(.callout)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .saturation(online ? 1 : 0)
            .opacity(online ? 1 : 0.6)
        } accessory: {
            if !online, let asOf {
                Text("As of \(Text(asOf, format: .dateTime.hour().minute()))",
                     comment: "Hardware page Now card while the device is offline: the time of the readings shown (\"As of 21:43\").")
            }
        }
    }

    private func gauge(_ g: HardwareGauge) -> some View {
        VStack(spacing: 8) {
            Gauge(value: min(max(g.value ?? g.range.lowerBound, g.range.lowerBound), g.range.upperBound), in: g.range) {
                Text(g.title)
            } currentValueLabel: {
                Text(verbatim: g.text ?? "—")
            }
            .gaugeStyle(HardwareRingGaugeStyle(tint: g.value == nil ? .secondary : g.tint,
                                               diameter: gauges.count > 4 ? 66 : 76))
            HStack(spacing: 3) {
                if g.warn {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .imageScale(.small)
                }
                Text(g.title)
            }
            .lineLimit(1)
            .minimumScaleFactor(0.85)
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(g.title)
        .accessibilityValue([g.text ?? String(localized: "Not available"),
                             g.warn ? String(localized: "Needs attention") : nil].compactMap { $0 }.joined(separator: ", "))
    }

    @ViewBuilder
    private func fact(_ f: HardwareFact, gap: Bool) -> some View {
        Text(f.title).foregroundStyle(.secondary)
        HStack(alignment: .firstTextBaseline, spacing: 4) {
            if f.warn {
                Image(systemName: "exclamationmark.triangle.fill").imageScale(.small).foregroundStyle(.orange)
            }
            Text(verbatim: f.value ?? "—")
                .monospacedDigit()
                .lineLimit(1)
                .foregroundStyle(f.warn ? AnyShapeStyle(.orange) : AnyShapeStyle(.primary))
            if let note = f.note {
                Text(verbatim: note).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .padding(.trailing, gap ? 16 : 0)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(f.title))
        .accessibilityValue(Text(verbatim: [f.value ?? String(localized: "Not available"), f.note,
                                            f.warn ? String(localized: "Needs attention") : nil].compactMap { $0 }
                                     .joined(separator: ", ")))
    }

    static func level(_ v: Double?, good: (Double) -> Bool, fair: (Double) -> Bool) -> Color {
        guard let v else { return .secondary }
        if good(v) { return .green }
        return fair(v) ? .yellow : .orange
    }

    static func wifi(rssi: Int?) -> HardwareGauge {
        let v = rssi.map(Double.init)
        return HardwareGauge(id: "wifi", title: "Wi-Fi", value: v, range: -90 ... -30,
                             text: v.map(HardwareFormat.dbm),
                             tint: level(v, good: { $0 >= Double(WiFiReadout.goodRSSI) }, fair: { $0 >= Double(WiFiReadout.weakRSSI) }),
                             warn: rssi.map { ClockHealthReadout.wifi(rssi: $0).weak } ?? false)
    }
}

/// Hand-drawn: the system accessory gauge style is fixed at watch-complication size.
struct HardwareRingGaugeStyle: GaugeStyle {
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
