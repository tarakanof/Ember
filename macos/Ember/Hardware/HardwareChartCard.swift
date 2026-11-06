import Accessibility
import Charts
import SwiftUI
import EmberKit

struct HardwareLine: Identifiable {
    let name: String
    let color: Color
    var dashed = false
    var id: String { name }
}

enum HardwarePalette {
    static let first = Color.blue
    static let second = Color.orange
    static let third = Color.purple
    static let fourth = Color.gray
}

enum HardwareChartStyle {
    case lines
    case bars(Calendar.Component)
}

struct HardwareChartCard: View {
    let title: LocalizedStringKey
    let axTitle: String
    let systemImage: String
    let points: [HardwareSeriesPoint]
    let lines: [HardwareLine]
    let range: HardwareRange
    let now: Date
    var headline: Double?
    var headlineText: String?
    var fixedDomain: ClosedRange<Double>?
    var zeroBaseline = false
    var interpolation: InterpolationMethod = .linear
    var style: HardwareChartStyle = .lines
    var warn = false
    let format: (Double) -> String

    @State private var selected: Date?

    var body: some View {
        DashboardCard(title: title, systemImage: systemImage) {
            if points.isEmpty {
                ContentUnavailableView("No data in this range", systemImage: "chart.xyaxis.line")
            } else {
                VStack(alignment: .leading, spacing: 6) {
                    if lines.count > 1 { legend }
                    chart
                }
            }
        } accessory: {
            if let text = headlineText ?? headline.map(format) {
                HStack(spacing: 3) {
                    if warn {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                            .accessibilityLabel(Text("Needs attention"))
                    }
                    Text(verbatim: text)
                        .foregroundStyle(warn ? AnyShapeStyle(.orange) : AnyShapeStyle(.secondary))
                }
            }
        }
    }

    private var domain: ClosedRange<Double> {
        if let fixedDomain { return fixedDomain }
        let values: [Double]
        if case .bars = style {
            values = Dictionary(grouping: points, by: \.t).values.map { $0.reduce(0) { $0 + $1.value } }
        } else {
            values = points.map(\.value)
        }
        var lo = values.min() ?? 0, hi = values.max() ?? 1
        let pad = Swift.max((hi - lo) * 0.15, Swift.max(abs(hi) * 0.02, 1))
        hi += pad
        lo = zeroBaseline || isBars ? 0 : lo - pad
        return lo...hi
    }

    private var isBars: Bool {
        if case .bars = style { return true }
        return false
    }

    private var xDomain: ClosedRange<Date> { now.addingTimeInterval(-range.duration)...now }

    private var chart: some View {
        let y = domain
        let primary = lines.first?.name
        let fills = zeroBaseline && !isBars
        return Chart {
            ForEach(points) { p in
                switch style {
                case .lines:
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
                case .bars(let unit):
                    BarMark(x: .value("Time", p.t, unit: unit), y: .value(axTitle, p.value), width: .ratio(0.7))
                        .foregroundStyle(color(p.series))
                }
            }
            if let sel = selected, let nearest = nearestTime(to: sel) {
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
        .accessibilityChartDescriptor(HardwareSeriesDescriptor(title: axTitle, points: points, lines: lines.map(\.name),
                                                               domain: y, continuous: !isBars, format: format))
    }

    private var legend: some View {
        HStack(spacing: 12) {
            ForEach(lines) { line in
                HStack(spacing: 5) {
                    if isBars {
                        RoundedRectangle(cornerRadius: 2).fill(line.color).frame(width: 10, height: 10)
                    } else {
                        Path { p in p.move(to: CGPoint(x: 0, y: 4)); p.addLine(to: CGPoint(x: 16, y: 4)) }
                            .stroke(line.color, style: stroke(line.name))
                            .frame(width: 16, height: 8)
                    }
                    Text(verbatim: line.name).font(.caption).foregroundStyle(.secondary).lineLimit(1).fixedSize()
                }
                .accessibilityElement(children: .combine)
            }
        }
    }

    private func stroke(_ series: String) -> StrokeStyle {
        let dashed = isDashed(series)
        return StrokeStyle(lineWidth: dashed ? 1.5 : 2, lineCap: .round, dash: dashed ? [4, 3] : [])
    }

    private func color(_ series: String) -> Color { lines.first { $0.name == series }?.color ?? HardwarePalette.first }
    private func isDashed(_ series: String) -> Bool { lines.first { $0.name == series }?.dashed ?? false }

    private func nearestTime(to date: Date) -> Date? {
        points.min { abs($0.t.timeIntervalSince(date)) < abs($1.t.timeIntervalSince(date)) }?.t
    }
}

struct HardwareSeriesDescriptor: AXChartDescriptorRepresentable {
    let title: String
    let points: [HardwareSeriesPoint]
    let lines: [String]
    let domain: ClosedRange<Double>
    var continuous = true
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
            AXDataSeriesDescriptor(name: name, isContinuous: continuous,
                                   dataPoints: points.filter { $0.series == name }
                                       .map { AXDataPoint(x: $0.t.timeIntervalSince1970, y: $0.value) })
        }
        let summary = lines.compactMap { name -> String? in
            let values = points.filter { $0.series == name }.map(\.value)
            guard let lo = values.min(), let hi = values.max(), let last = values.last else { return nil }
            return String(localized: "\(name): now \(format(last)), range \(format(lo)) to \(format(hi)).",
                          comment: "Hardware chart VoiceOver summary: series name, latest value, lowest and highest values.")
        }.joined(separator: " ")
        return AXChartDescriptor(title: title, summary: summary, xAxis: x, yAxis: y, additionalAxes: [], series: series)
    }
}

enum HardwareFormat {
    static func percent(_ v: Double) -> String { Percent.text(v) }

    static func bytes(_ v: Double) -> String {
        bytes(scaleTo: v)(v)
    }

    static func bytes(scaleTo largest: Int) -> (Double) -> String { bytes(scaleTo: Double(largest)) }

    static func bytes(scaleTo largest: Double) -> (Double) -> String {
        let f = ByteCountFormatter()
        f.countStyle = .decimal
        f.allowsNonnumericFormatting = false
        f.allowedUnits = largest >= 1_000_000 ? .useMB : .useKB
        return { v in f.string(fromByteCount: Int64(v.rounded())) }
    }

    static func celsius(_ v: Double) -> String {
        Measurement(value: v, unit: UnitTemperature.celsius)
            .formatted(.measurement(width: .narrow, usage: .asProvided, numberFormatStyle: .number.precision(.fractionLength(0...1))))
    }

    static func dbm(_ v: Double) -> String {
        String(localized: "\(Int(v.rounded())) dBm", comment: "Wi-Fi signal strength in decibel-milliwatts (\"-62 dBm\").")
    }

    static func lux(_ v: Double) -> String {
        String(localized: "\(v.formatted(.number.precision(.fractionLength(0)))) lx",
               comment: "A light level in lux (\"42 lx\").")
    }

    static func count(_ v: Double) -> String { v.formatted(.number.precision(.fractionLength(0...1))) }

    static func perMinute(_ v: Double) -> String {
        String(localized: "\(v.formatted(.number.precision(.fractionLength(0...1))))/min",
               comment: "A rate per minute (\"31/min\").")
    }

    static func milliseconds(_ v: Double) -> String {
        String(localized: "\(Int(v.rounded())) ms", comment: "A duration in milliseconds (\"38 ms\").")
    }

    static func fps(_ v: Double) -> String {
        String(localized: "\(v.formatted(.number.precision(.fractionLength(0...1)))) fps",
               comment: "Frames per second (\"29.5 fps\").")
    }
}
