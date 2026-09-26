import Charts
import SwiftUI
import EmberKit

/// Card 10: how long agents worked each day, stacked by the Mac they ran on.
struct AgentTimeCard: View {
    let activity: Loadable<ActivitySummary>
    var calendar = Calendar.current

    var body: some View {
        let feed = activity.map { AgentTimeChart(summary: $0, calendar: calendar) }
        DashboardCard(title: "Agent time", systemImage: "cpu") {
            FeedStateView(feed: feed, placeholder: AgentTimeChart(summary: DashboardPlaceholders.activity, calendar: calendar),
                          isEmpty: \.isEmpty,
                          emptyTitle: "No agent activity this week", emptySymbol: "cpu",
                          offTitle: ServerRequirement.title, offSymbol: ServerRequirement.symbol) { c in
                FocusTintReader { tint in chart(c, redacted: tint.redacted) }
            }
        } accessory: {
            if let c = feed.value, !c.isEmpty {
                Text("Today \(DurationText.minutes(Int(c.todayMinutes.rounded())))")
            }
        }
    }

    /// The hovered day's key ("2026-09-26").
    @State private var selected: String?

    /// Dates in the card's calendar: the day keys are that calendar's days.
    private var dayStyle: Date.FormatStyle {
        var style = Date.FormatStyle(date: .omitted, time: .omitted).locale(calendar.locale ?? .current)
        style.timeZone = calendar.timeZone
        return style
    }

    /// Days are categories (their "2026-09-26" keys), not dates: a band
    /// scale centres every label under its bar by construction. A date
    /// axis centred its labels between ticks, and the last day, whose
    /// closing tick sits on the plot edge, drew its label off-centre.
    private func chart(_ c: AgentTimeChart, redacted: Bool) -> some View {
        let top = WeekBars.axisTop(Int((c.dailyTotals.map(\.minutes).max() ?? 0).rounded(.up)))
        let dates = Dictionary(c.segments.map { ($0.key, $0.date) }, uniquingKeysWith: { a, _ in a })
        return Chart {
            ForEach(c.segments) { s in
                BarMark(x: .value("Day", s.key),
                        y: .value("Active minutes", s.minutes),
                        width: .ratio(0.62))
                    .foregroundStyle(by: .value("Source", s.source))
                    .opacity(selected == nil || selected == s.key ? 1 : 0.5)
                    // The x value is the ISO day key; speak the date.
                    .accessibilityLabel(Text(s.date, format: dayStyle.weekday(.wide).day()))
            }
            if let key = selected, let day = c.detail(forKey: key) {
                RuleMark(x: .value("Day", key))
                    .foregroundStyle(.clear)
                    .annotation(position: .top, overflowResolution: .init(x: .fit(to: .plot), y: .fit(to: .chart))) {
                        ChartCallout {
                            Text(day.date, format: dayStyle.weekday(.wide).day())
                            Text(verbatim: DurationText.minutes(Int(day.totalMinutes.rounded())))
                            ForEach(day.parts.prefix(4), id: \.source) { part in
                                Text(verbatim: "\(part.source) \(DurationText.minutes(Int(part.minutes.rounded())))")
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
            }
        }
        .chartForegroundStyleScale(domain: c.sources.map(\.name),
                                   range: c.sources.enumerated().map { i, s in
                                       redacted ? Color.secondary.opacity(i == 0 ? 0.35 : 0.2)
                                                : EmberColors.hex(s.colorHex, fallback: .accentColor)
                                   })
        .chartYScale(domain: 0...top)
        .chartXScale(domain: c.dayKeys)
        .chartXAxis {
            AxisMarks(values: c.dayKeys) { value in
                AxisValueLabel {
                    if let key = value.as(String.self), let d = dates[key] {
                        Text(d, format: dayStyle.weekday(.narrow))
                    }
                }
            }
        }
        .chartXSelection(value: $selected)
        .chartYAxis {
            AxisMarks(position: .leading, values: .stride(by: Double(minutesStride(top)))) { value in
                AxisGridLine()
                AxisValueLabel { if let v = value.as(Int.self) { Text(minutesAxisLabel(v)) } }
            }
        }
        .chartLegend(position: .bottom, alignment: .leading, spacing: 6)
        .accessibilityLabel("Agent active time per day by Mac")
        .accessibilityChartDescriptor(AgentTimeDescriptor(chart: c))
        .overlay(alignment: .topTrailing) {
            if !c.isRecording {
                Label("Not recording", systemImage: "pause.circle")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .help("The server's activity overlay is off, so new agent time isn't stored.")
            }
        }
    }
}
