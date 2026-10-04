import Accessibility
import Charts
import SwiftUI
import EmberKit

/// One named line on a knob chart: its legend name, colour and stroke.
struct KnobLine: Identifiable {
    let name: String
    let color: Color
    var dashed = false
    var value: (KnobStats.Sample) -> Double?
    var id: String { name }
}

/// Series colours: a categorical set with no good/bad meaning, apart for
/// common colour-vision deficiencies (blue, orange, purple, grey).
enum KnobPalette {
    static let first = Color.blue
    static let second = Color.orange
    static let third = Color.purple
    static let fourth = Color.gray
}

/// A knob time-series card: lines over the selected range, the newest value
/// in the accessory, a hover callout and an audio graph.
struct KnobChartCard: View {
    let title: LocalizedStringKey
    let axTitle: String
    let systemImage: String
    let stats: KnobStats
    let lines: [KnobLine]
    let range: KnobStatsRange
    let now: Date
    /// The accessory's value; defaults to the first line's newest value.
    var headline: Double?
    /// A fixed y range (CPU 0–100 %); nil fits the data.
    var fixedDomain: ClosedRange<Double>?
    /// Fitted axes start at zero (bytes, rates); the first line is then
    /// filled down to it. Without a zero baseline nothing is filled.
    var zeroBaseline = false
    /// `.stepEnd` for integer readings such as dBm.
    var interpolation: InterpolationMethod = .linear
    var warn = false
    let format: (Double) -> String

    @State private var selected: Date?

    private var points: [KnobSeriesPoint] {
        stats.series(lines.map { line in (line.name, line.value) }, range: range)
    }

    var body: some View {
        let points = points
        DashboardCard(title: title, systemImage: systemImage) {
            if points.isEmpty {
                ContentUnavailableView("No data in this range", systemImage: "chart.xyaxis.line")
            } else {
                VStack(alignment: .leading, spacing: 6) {
                    if lines.count > 1 { legend }
                    chart(points)
                }
            }
        } accessory: {
            if let v = headline ?? stats.latest.flatMap({ lines.first?.value($0) }) {
                HStack(spacing: 3) {
                    if warn {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                            .accessibilityLabel(Text("Needs attention"))
                    }
                    Text(format(v))
                        .foregroundStyle(warn ? AnyShapeStyle(.orange) : AnyShapeStyle(.secondary))
                }
            }
        }
    }

    private func domain(_ points: [KnobSeriesPoint]) -> ClosedRange<Double> {
        if let fixedDomain { return fixedDomain }
        let values = points.map(\.value)
        var lo = values.min() ?? 0, hi = values.max() ?? 1
        let pad = Swift.max((hi - lo) * 0.15, Swift.max(abs(hi) * 0.02, 1))
        hi += pad
        lo = zeroBaseline ? 0 : lo - pad
        return lo...hi
    }

    private var xDomain: ClosedRange<Date> { now.addingTimeInterval(-range.duration)...now }

    private func chart(_ points: [KnobSeriesPoint]) -> some View {
        let y = domain(points)
        let primary = lines.first?.name
        let fills = zeroBaseline
        return Chart {
            ForEach(points) { p in
                if fills, p.series == primary {
                    AreaMark(x: .value("Time", p.t), yStart: .value("Base", 0),
                             yEnd: .value(axTitle, p.value), series: .value("Line", p.lineKey))
                        .interpolationMethod(interpolation)
                        .foregroundStyle(LinearGradient(colors: [color(p.series).opacity(0.22), color(p.series).opacity(0.02)],
                                                        startPoint: .top, endPoint: .bottom))
                }
                LineMark(x: .value("Time", p.t), y: .value(axTitle, p.value), series: .value("Line", p.lineKey))
                    .interpolationMethod(interpolation)
                    .foregroundStyle(color(p.series))
                    .lineStyle(stroke(p.series))
            }
            if let sel = selected, let nearest = nearestTime(to: sel, in: points) {
                RuleMark(x: .value("Time", nearest))
                    .foregroundStyle(.secondary.opacity(0.5))
                    .annotation(position: .top, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                        ChartCallout {
                            Text(nearest, format: range == .day ? .dateTime.hour().minute() : .dateTime.hour().minute().second())
                                .foregroundStyle(.secondary)
                            ForEach(points.filter { $0.t == nearest }) { p in
                                Text(verbatim: lines.count > 1 ? "\(p.series): \(format(p.value))" : format(p.value))
                            }
                        }
                    }
            }
        }
        .chartLegend(.hidden)
        .chartXScale(domain: xDomain)
        .chartYScale(domain: y)
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: 4)) { _ in
                AxisGridLine()
                AxisValueLabel(format: .dateTime.hour().minute())
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: .automatic(desiredCount: 4)) { value in
                AxisGridLine()
                AxisValueLabel { if let v = value.as(Double.self) { Text(verbatim: format(v)) } }
            }
        }
        .chartXSelection(value: $selected)
        .accessibilityLabel(Text(title))
        .accessibilityChartDescriptor(KnobSeriesDescriptor(title: axTitle, points: points, lines: lines.map(\.name),
                                                           domain: y, format: format))
    }

    /// The legend draws each line's own stroke, dashes included.
    private var legend: some View {
        HStack(spacing: 12) {
            ForEach(lines) { line in
                HStack(spacing: 5) {
                    Path { p in p.move(to: CGPoint(x: 0, y: 4)); p.addLine(to: CGPoint(x: 16, y: 4)) }
                        .stroke(line.color, style: stroke(line.name))
                        .frame(width: 16, height: 8)
                    Text(verbatim: line.name).font(.caption).foregroundStyle(.secondary)
                }
                .accessibilityElement(children: .combine)
            }
        }
    }

    private func stroke(_ series: String) -> StrokeStyle {
        let dashed = isDashed(series)
        return StrokeStyle(lineWidth: dashed ? 1.5 : 2, lineCap: .round, dash: dashed ? [4, 3] : [])
    }

    private func color(_ series: String) -> Color { lines.first { $0.name == series }?.color ?? KnobPalette.first }
    private func isDashed(_ series: String) -> Bool { lines.first { $0.name == series }?.dashed ?? false }

    private func nearestTime(to date: Date, in points: [KnobSeriesPoint]) -> Date? {
        points.min { abs($0.t.timeIntervalSince(date)) < abs($1.t.timeIntervalSince(date)) }?.t
    }
}

/// VoiceOver chart description and audio graph for a knob series chart.
struct KnobSeriesDescriptor: AXChartDescriptorRepresentable {
    let title: String
    let points: [KnobSeriesPoint]
    let lines: [String]
    let domain: ClosedRange<Double>
    let format: (Double) -> String

    func makeChartDescriptor() -> AXChartDescriptor {
        let times = points.map(\.t)
        let start = times.min() ?? Date(), end = times.max() ?? Date()
        let x = AXNumericDataAxisDescriptor(title: String(localized: "Time"),
                                            range: start.timeIntervalSince1970...max(end.timeIntervalSince1970, start.timeIntervalSince1970 + 1),
                                            gridlinePositions: []) {
            Date(timeIntervalSince1970: $0).formatted(date: .omitted, time: .shortened)
        }
        let y = AXNumericDataAxisDescriptor(title: title, range: domain, gridlinePositions: [], valueDescriptionProvider: format)
        let series = lines.map { name in
            AXDataSeriesDescriptor(name: name, isContinuous: true,
                                   dataPoints: points.filter { $0.series == name }
                                       .map { AXDataPoint(x: $0.t.timeIntervalSince1970, y: $0.value) })
        }
        let summary = lines.compactMap { name -> String? in
            let values = points.filter { $0.series == name }.map(\.value)
            guard let lo = values.min(), let hi = values.max(), let last = values.last else { return nil }
            return String(localized: "\(name): now \(format(last)), range \(format(lo)) to \(format(hi)).",
                          comment: "Knob chart VoiceOver summary: series name, latest value, lowest and highest values.")
        }.joined(separator: " ")
        return AXChartDescriptor(title: title, summary: summary, xAxis: x, yAxis: y, additionalAxes: [], series: series)
    }
}
