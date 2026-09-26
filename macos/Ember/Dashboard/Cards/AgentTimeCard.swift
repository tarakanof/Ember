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

    private func chart(_ c: AgentTimeChart, redacted: Bool) -> some View {
        let top = WeekBars.axisTop(Int((c.dailyTotals.map(\.minutes).max() ?? 0).rounded(.up)))
        return Chart(c.segments) { s in
            BarMark(x: .value("Day", s.date, unit: .day),
                    y: .value("Active minutes", s.minutes),
                    width: .ratio(0.62))
                .foregroundStyle(by: .value("Source", s.source))
        }
        .chartForegroundStyleScale(domain: c.sources.map(\.name),
                                   range: c.sources.enumerated().map { i, s in
                                       redacted ? Color.secondary.opacity(i == 0 ? 0.35 : 0.2)
                                                : EmberColors.hex(s.colorHex, fallback: .accentColor)
                                   })
        .chartYScale(domain: 0...top)
        .chartXScale(domain: DayRange.domain(c.days, calendar: calendar))
        .chartXAxis {
            // The day after the last is listed too: a centred label needs
            // the next tick to span to, or the last day's is dropped.
            AxisMarks(values: DayRange.ticks(c.days, calendar: calendar)) { value in
                if let d = value.as(Date.self), d < DayRange.domain(c.days, calendar: calendar).upperBound {
                    AxisValueLabel(format: .dateTime.weekday(.narrow), centered: true)
                }
            }
        }
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
