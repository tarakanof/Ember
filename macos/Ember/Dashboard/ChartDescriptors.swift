import Accessibility
import SwiftUI
import EmberKit

// Audio graphs and VoiceOver chart summaries for the Dashboard charts
// (design §2.7). Each describes the same numbers the chart draws.

struct WeekBarsDescriptor: AXChartDescriptorRepresentable {
    let bars: WeekBars

    func makeChartDescriptor() -> AXChartDescriptor {
        let names = bars.bars.map { $0.date.formatted(.dateTime.weekday(.wide)) }
        let x = AXCategoricalDataAxisDescriptor(title: String(localized: "Day"), categoryOrder: names)
        let y = AXNumericDataAxisDescriptor(title: String(localized: "Focus minutes"),
                                            range: 0...Double(bars.yMax), gridlinePositions: []) {
            DurationText.minutes(Int($0))
        }
        let series = AXDataSeriesDescriptor(name: String(localized: "Focus"), isContinuous: false,
                                            dataPoints: zip(names, bars.bars).map { AXDataPoint(x: $0, y: Double($1.focusMin)) })
        var summary = String(localized: "\(DurationText.minutes(bars.totalMinutes)) of focus over 7 days.")
        if let goal = bars.goalMinutes, let met = bars.daysAtGoal {
            summary += " " + inflected("Goal \(DurationText.minutes(goal)) a day, met on ^[\(met) day](inflect: true).")
        }
        return AXChartDescriptor(title: String(localized: "Last 7 days"), summary: summary,
                                 xAxis: x, yAxis: y, additionalAxes: [], series: [series])
    }
}

struct TrendDescriptor: AXChartDescriptorRepresentable {
    let trend: WeeklyTrend

    func makeChartDescriptor() -> AXChartDescriptor {
        let names = trend.points.map { String(localized: "Week of \($0.weekStart.formatted(.dateTime.month(.abbreviated).day()))") }
        let x = AXCategoricalDataAxisDescriptor(title: String(localized: "Week"), categoryOrder: names)
        let y = AXNumericDataAxisDescriptor(title: String(localized: "Focus minutes"),
                                            range: 0...Double(trend.yMax), gridlinePositions: []) {
            DurationText.minutes(Int($0))
        }
        let series = AXDataSeriesDescriptor(name: String(localized: "Focus"), isContinuous: true,
                                            dataPoints: zip(names, trend.points).map { AXDataPoint(x: $0, y: Double($1.focusMin)) })
        let summary = trend.averageMinutes.map {
            String(localized: "Average \(DurationText.minutes($0)) a week.")
        }
        return AXChartDescriptor(title: String(localized: "Focus per week"), summary: summary,
                                 xAxis: x, yAxis: y, additionalAxes: [], series: [series])
    }
}

struct WorkHoursDescriptor: AXChartDescriptorRepresentable {
    let chart: WorkHoursChart
    let label: (WorkHoursChart.Row) -> String
    let summary: (WorkHoursChart.Row) -> String

    func makeChartDescriptor() -> AXChartDescriptor {
        let names = chart.rows.map(label)
        let x = AXCategoricalDataAxisDescriptor(title: String(localized: "Day"), categoryOrder: names)
        let hour: (Double) -> String = { value in
            let minutes = Int((value * 60).rounded())
            let d = Calendar.current.date(bySettingHour: (minutes / 60) % 24, minute: minutes % 60, second: 0,
                                          of: DashboardReference.day) ?? DashboardReference.day
            return d.formatted(date: .omitted, time: .shortened)
        }
        let y = AXNumericDataAxisDescriptor(title: String(localized: "Hour of day"),
                                            range: chart.hourDomain, gridlinePositions: [], valueDescriptionProvider: hour)
        let starts = AXDataSeriesDescriptor(name: String(localized: "Start"), isContinuous: false,
                                            dataPoints: chart.rows.map { AXDataPoint(x: label($0), y: $0.start, label: summary($0)) })
        let ends = AXDataSeriesDescriptor(name: String(localized: "End"), isContinuous: false,
                                          dataPoints: chart.rows.map { AXDataPoint(x: label($0), y: $0.end) })
        return AXChartDescriptor(title: String(localized: "Work hours"), summary: nil,
                                 xAxis: x, yAxis: y, additionalAxes: [], series: [starts, ends])
    }
}

struct HeatmapDescriptor: AXChartDescriptorRepresentable {
    let grid: HeatmapGrid
    let hourName: (Int) -> String

    func makeChartDescriptor() -> AXChartDescriptor {
        let hours = (0..<24).map(hourName)
        let x = AXCategoricalDataAxisDescriptor(title: String(localized: "Hour"), categoryOrder: hours)
        let y = AXNumericDataAxisDescriptor(title: String(localized: "Focus minutes"),
                                            range: 0...Double(max(grid.maxMinutes, 1)), gridlinePositions: []) {
            String(localized: "\(Int($0)) min")
        }
        let series = grid.rowLabels.enumerated().map { row, name in
            AXDataSeriesDescriptor(name: name, isContinuous: false,
                                   dataPoints: grid.cells.filter { $0.row == row }
                                       .map { AXDataPoint(x: hours[$0.hour], y: Double($0.minutes)) })
        }
        let summary = grid.peak.map {
            String(localized: "Most focus on \(grid.rowLabels[$0.row]) at \(hours[$0.hour]).")
        }
        return AXChartDescriptor(title: String(localized: "When you focus"), summary: summary,
                                 xAxis: x, yAxis: y, additionalAxes: [], series: series)
    }
}

struct AgentTimeDescriptor: AXChartDescriptorRepresentable {
    let chart: AgentTimeChart

    func makeChartDescriptor() -> AXChartDescriptor {
        let names = chart.days.map { $0.formatted(.dateTime.weekday(.wide)) }
        let x = AXCategoricalDataAxisDescriptor(title: String(localized: "Day"), categoryOrder: names)
        let top = max(1, chart.dailyTotals.map(\.minutes).max() ?? 1)
        let y = AXNumericDataAxisDescriptor(title: String(localized: "Active minutes"),
                                            range: 0...top, gridlinePositions: []) {
            DurationText.minutes(Int($0))
        }
        let series = chart.sources.map { src in
            AXDataSeriesDescriptor(name: src.name, isContinuous: false,
                                   dataPoints: zip(names, chart.days).map { name, day in
                                       AXDataPoint(x: name, y: chart.segments.first { $0.date == day && $0.source == src.name }?.minutes ?? 0)
                                   })
        }
        return AXChartDescriptor(title: String(localized: "Agent time"),
                                 summary: String(localized: "\(DurationText.minutes(Int(chart.totalMinutes))) over 7 days."),
                                 xAxis: x, yAxis: y, additionalAxes: [], series: series)
    }
}

/// A localized string with automatic grammar agreement ("1 day", "2 days").
func inflected(_ resource: LocalizedStringResource) -> String {
    String(AttributedString(localized: resource).characters)
}
