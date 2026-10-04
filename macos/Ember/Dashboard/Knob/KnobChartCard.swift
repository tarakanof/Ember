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

/// A knob time-series card: smooth lines over the selected range, the
/// newest value in the accessory, a hover callout and an audio graph.
struct KnobChartCard: View {
    let title: LocalizedStringKey
    let axTitle: String
    let systemImage: String
    let stats: KnobStats
    let lines: [KnobLine]
    let range: KnobStatsRange
    let now: Date
    /// A fixed y range (CPU 0–100 %); nil fits the data.
    var fixedDomain: ClosedRange<Double>?
    /// Never let the fitted y range start above this (bytes, rates).
    var floor: Double?
    var fillsArea = true
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
                chart(points)
            }
        } accessory: {
            if let latest = stats.latest, let v = lines.first?.value(latest) {
                Text(format(v))
                    .foregroundStyle(warn ? AnyShapeStyle(.orange) : AnyShapeStyle(.secondary))
            }
        }
    }

    private func domain(_ points: [KnobSeriesPoint]) -> ClosedRange<Double> {
        if let fixedDomain { return fixedDomain }
        let values = points.map(\.value)
        var lo = values.min() ?? 0, hi = values.max() ?? 1
        if let floor { lo = Swift.min(lo, floor) }
        let pad = Swift.max((hi - lo) * 0.15, Swift.max(abs(hi) * 0.02, 1))
        if floor == nil || lo > floor! { lo -= pad }
        hi += pad
        return lo...hi
    }

    private var xDomain: ClosedRange<Date> { now.addingTimeInterval(-range.duration)...now }

    private func chart(_ points: [KnobSeriesPoint]) -> some View {
        let y = domain(points)
        let primary = lines.first?.name
        return Chart {
            ForEach(points) { p in
                if fillsArea, p.series == primary {
                    AreaMark(x: .value("Time", p.t), yStart: .value("Base", y.lowerBound),
                             yEnd: .value(axTitle, p.value), series: .value("Line", p.lineKey))
                        .interpolationMethod(.monotone)
                        .foregroundStyle(LinearGradient(colors: [color(p.series).opacity(0.24), color(p.series).opacity(0.02)],
                                                        startPoint: .top, endPoint: .bottom))
                }
                LineMark(x: .value("Time", p.t), y: .value(axTitle, p.value), series: .value("Line", p.lineKey))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(by: .value("Series", p.series))
                    .lineStyle(StrokeStyle(lineWidth: isDashed(p.series) ? 1.25 : 2, lineCap: .round,
                                           dash: isDashed(p.series) ? [4, 3] : []))
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
        .chartForegroundStyleScale(domain: lines.map(\.name), range: lines.map(\.color))
        .chartLegend(lines.count > 1 ? .visible : .hidden)
        .chartLegend(position: .top, alignment: .leading, spacing: 6)
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

    private func color(_ series: String) -> Color { lines.first { $0.name == series }?.color ?? .accentColor }
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
