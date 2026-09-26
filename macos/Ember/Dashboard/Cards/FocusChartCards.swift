import Charts
import SwiftUI
import EmberKit

/// Minutes on a chart axis: "30m", "1h", "1h 30m".
func minutesAxisLabel(_ minutes: Int) -> String { DurationText.minutes(minutes) }

/// Axis stride for a minutes axis topping out at `top`.
func minutesStride(_ top: Int) -> Int {
    switch top {
    case ...90: 30
    case ...360: 60
    case ...720: 120
    default: 240
    }
}

/// A time of day as the user's clock writes it: "11:00" or "11:00 AM".
func timeOfDayLabel(_ hour: Int, calendar: Calendar) -> Text {
    let date = calendar.date(bySettingHour: ((hour % 24) + 24) % 24, minute: 0, second: 0,
                             of: DashboardReference.day) ?? DashboardReference.day
    var style = Date.FormatStyle(date: .omitted, time: .shortened)
    style.timeZone = calendar.timeZone
    return Text(date, format: style.locale(calendar.locale ?? .current))
}

/// An hour of the day as the locale writes it on an axis: "09" or "9 AM".
func hourLabel(_ hour: Int, calendar: Calendar) -> Text {
    let h = ((hour % 24) + 24) % 24
    let date = calendar.date(bySettingHour: h, minute: 0, second: 0, of: DashboardReference.day) ?? DashboardReference.day
    var style = Date.FormatStyle(date: .omitted, time: .omitted).hour(.defaultDigits(amPM: .abbreviated))
    style.timeZone = calendar.timeZone
    return Text(date, format: style.locale(calendar.locale ?? .current))
}

enum DashboardReference {
    /// A fixed day to hang hour-of-day labels on.
    static let day = Date(timeIntervalSinceReferenceDate: 800_000_000)
}

/// The focus charts' colours: the accent for today, a lighter accent for
/// past days (lighter still in light mode, where it has more contrast to
/// spare), grey while a placeholder is redacted.
struct FocusTint {
    let scheme: ColorScheme
    let redacted: Bool

    var current: Color { redacted ? .secondary.opacity(0.35) : .accentColor }
    var past: Color {
        redacted ? .secondary.opacity(0.2) : .accentColor.opacity(scheme == .dark ? 0.62 : 0.45)
    }
    /// The heatmap's sequential ramp, in the same accent family.
    func heat(_ minutes: Int, max: Int) -> Color {
        guard max > 0, minutes > 0 else { return Color.secondary.opacity(0.12) }
        if redacted { return .secondary.opacity(0.3) }
        let f = Double(minutes) / Double(max)
        return Color.accentColor.opacity((scheme == .dark ? 0.3 : 0.2) + (scheme == .dark ? 0.7 : 0.8) * f)
    }
}

struct FocusTintReader<Content: View>: View {
    @ViewBuilder let content: (FocusTint) -> Content
    @Environment(\.colorScheme) private var scheme
    @Environment(\.redactionReasons) private var redaction
    var body: some View { content(FocusTint(scheme: scheme, redacted: !redaction.isEmpty)) }
}

/// The Pomodoro statistics cards share one off copy: both endpoints are 404
/// when Pomodoro is off.
@MainActor
private func focusStatsState<T, C: View>(_ feed: Loadable<T>, placeholder: T?, isEmpty: @escaping (T) -> Bool,
                                         @ViewBuilder content: @escaping (T) -> C) -> some View {
    FeedStateView(feed: feed, placeholder: placeholder, isEmpty: isEmpty, emptyTitle: "No data yet",
                  emptySymbol: "chart.bar",
                  offTitle: "Pomodoro is off", offDescription: "Turn it on in Settings › Focus.",
                  offSettingsPane: "focus", content: content)
}

// MARK: Card 6 — Last 7 days

struct LastSevenDaysCard: View {
    let stats: Loadable<PomoStats>
    var focusMinutes: Int?
    var calendar = Calendar.current

    @State private var selected: Date?

    var body: some View {
        let feed = stats.map { WeekBars(stats: $0, focusMinutes: focusMinutes, calendar: calendar) }
        DashboardCard(title: "Last 7 days", systemImage: "chart.bar") {
            focusStatsState(feed, placeholder: WeekBars(stats: DashboardPlaceholders.stats, focusMinutes: 25, calendar: calendar),
                            isEmpty: \.isEmpty) { bars in
                FocusTintReader { tint in chart(bars, tint: tint) }
            }
        } accessory: {
            if let bars = feed.value, !bars.isEmpty {
                Text(DurationText.minutes(bars.totalMinutes))
            }
        }
    }

    private func chart(_ w: WeekBars, tint: FocusTint) -> some View {
        Chart {
            ForEach(w.bars) { bar in
                BarMark(x: .value("Day", bar.date, unit: .day),
                        y: .value("Focus minutes", bar.focusMin),
                        width: .ratio(0.62))
                    .foregroundStyle(bar.isToday ? tint.current : tint.past)
                    .clipShape(RoundedRectangle(cornerRadius: 3, style: .continuous))
                    .opacity(selected == nil || calendar.isDate(bar.date, inSameDayAs: selected!) ? 1 : 0.5)
            }
            if let goal = w.goalMinutes {
                RuleMark(y: .value("Goal", goal))
                    .foregroundStyle(.secondary)
                    .lineStyle(StrokeStyle(lineWidth: 1, dash: [4, 3]))
                    .annotation(position: .top, alignment: .trailing, spacing: 2) {
                        Text("Goal").font(.caption2).foregroundStyle(.secondary)
                    }
            }
            if let sel = selected, let bar = w.bars.first(where: { calendar.isDate($0.date, inSameDayAs: sel) }) {
                RuleMark(x: .value("Day", bar.date, unit: .day))
                    .foregroundStyle(.clear)
                    .annotation(position: .top, overflowResolution: .init(x: .fit(to: .chart), y: .disabled)) {
                        ChartCallout {
                            Text(bar.date, format: .dateTime.weekday(.wide).day())
                            Text("\(DurationText.minutes(bar.focusMin)) · ^[\(bar.sessions) session](inflect: true)")
                        }
                    }
            }
        }
        .chartYScale(domain: 0...w.yMax)
        .chartXScale(domain: DayRange.domain(w.bars.map(\.date), calendar: calendar))
        .chartXAxis {
            // The day after the last is listed too: a centred label needs
            // the next tick to span to, or the last day's is dropped.
            AxisMarks(values: DayRange.ticks(w.bars.map(\.date), calendar: calendar)) { value in
                if let d = value.as(Date.self), d < DayRange.domain(w.bars.map(\.date), calendar: calendar).upperBound {
                    AxisValueLabel(format: .dateTime.weekday(.narrow), centered: true)
                }
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: .stride(by: Double(minutesStride(w.yMax)))) { value in
                AxisGridLine()
                AxisValueLabel { if let v = value.as(Int.self) { Text(minutesAxisLabel(v)) } }
            }
        }
        .chartXSelection(value: $selected)
        .accessibilityLabel("Focus minutes, last 7 days")
        .accessibilityChartDescriptor(WeekBarsDescriptor(bars: w))
    }
}

// MARK: Card 7 — 12 weeks

struct TwelveWeeksCard: View {
    let stats: Loadable<PomoStats>
    var now = Date()
    var calendar = Calendar.current

    @State private var selected: Date?

    private func model() -> Loadable<WeeklyTrend> {
        if let s = stats.value, WeeklyTrend.serverLacksWeekly(s) {
            return .failed(.featureOff, last: nil, lastAt: nil)
        }
        return stats.map { WeeklyTrend(weekly: $0.weekly, now: now, calendar: calendar) }
    }

    var body: some View {
        let feed = model()
        let placeholder = WeeklyTrend(weekly: DashboardPlaceholders.stats.weekly, now: now, calendar: calendar)
        DashboardCard(title: "12 weeks", systemImage: "chart.line.uptrend.xyaxis") {
            if stats.error == .featureOff {
                focusStatsState(feed, placeholder: placeholder, isEmpty: \.isEmpty) { t in chart(t) }
            } else {
                // featureOff here means the server predates weekly totals.
                FeedStateView(feed: feed, placeholder: placeholder, isEmpty: \.isEmpty,
                              emptyTitle: "No data yet", emptySymbol: "chart.bar",
                              offTitle: ServerRequirement.title, offSymbol: ServerRequirement.symbol) { t in chart(t) }
            }
        } accessory: {
            if let avg = feed.value?.averageMinutes {
                Text("avg \(DurationText.minutes(avg)) / week")
                    .help("Average of the finished weeks; this week is still filling up.")
            }
        }
    }

    private func chart(_ t: WeeklyTrend) -> some View {
        FocusTintReader { tint in
            Chart {
                ForEach(t.points) { p in
                    AreaMark(x: .value("Week", p.weekStart, unit: .weekOfYear),
                             y: .value("Focus minutes", p.focusMin))
                        .interpolationMethod(.monotone)
                        .foregroundStyle(LinearGradient(colors: [tint.current.opacity(0.28), tint.current.opacity(0.02)],
                                                        startPoint: .top, endPoint: .bottom))
                    LineMark(x: .value("Week", p.weekStart, unit: .weekOfYear),
                             y: .value("Focus minutes", p.focusMin))
                        .interpolationMethod(.monotone)
                        .foregroundStyle(tint.current)
                        .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round))
                }
                if let avg = t.averageMinutes {
                    RuleMark(y: .value("Average", avg))
                        .foregroundStyle(.secondary)
                        .lineStyle(StrokeStyle(lineWidth: 1, dash: [4, 3]))
                }
                if let last = t.last {
                    // This week, still in progress: a hollow point.
                    PointMark(x: .value("Week", last.weekStart, unit: .weekOfYear),
                              y: .value("Focus minutes", last.focusMin))
                        .symbol(.circle.strokeBorder(lineWidth: 2))
                        .foregroundStyle(tint.current)
                        .symbolSize(50)
                        .annotation(position: .top, alignment: .trailing, spacing: 4) {
                            Text("\(DurationText.minutes(last.focusMin)) so far")
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(.primary)
                        }
                }
                if let sel = selected,
                   let p = t.points.min(by: { abs($0.weekStart.timeIntervalSince(sel)) < abs($1.weekStart.timeIntervalSince(sel)) }) {
                    RuleMark(x: .value("Week", p.weekStart, unit: .weekOfYear))
                        .foregroundStyle(.secondary.opacity(0.4))
                        .annotation(position: .top, overflowResolution: .init(x: .fit(to: .chart), y: .disabled)) {
                            ChartCallout {
                                Text("Week of \(p.weekStart, format: .dateTime.month(.abbreviated).day())")
                                Text("\(DurationText.minutes(p.focusMin)) · ^[\(p.sessions) session](inflect: true)")
                            }
                        }
                }
            }
            .chartYScale(domain: 0...t.yMax)
            .chartXAxis {
                AxisMarks(values: .stride(by: .weekOfYear, count: t.points.count > 6 ? 3 : 1)) { _ in
                    AxisGridLine()
                    AxisValueLabel(format: .dateTime.month(.abbreviated).day())
                }
            }
            .chartYAxis {
                AxisMarks(position: .leading, values: .stride(by: Double(minutesStride(t.yMax)))) { value in
                    AxisGridLine()
                    AxisValueLabel { if let v = value.as(Int.self) { Text(minutesAxisLabel(v)) } }
                }
            }
            .chartXSelection(value: $selected)
            .accessibilityLabel("Focus minutes per week")
            .accessibilityChartDescriptor(TrendDescriptor(trend: t))
        }
    }
}

// MARK: Card 8 — Work hours

struct WorkHoursCard: View {
    let workhours: Loadable<WorkHours>
    var now = Date()
    var calendar = Calendar.current

    var body: some View {
        let feed = workhours.map { WorkHoursChart(days: $0.days, now: now, calendar: calendar) }
        DashboardCard(title: "Work hours", systemImage: "clock.arrow.2.circlepath", height: DashboardCardHeight.wide) {
            focusStatsState(feed, placeholder: WorkHoursChart(days: DashboardPlaceholders.workhours.days, now: now,
                                                              calendar: calendar),
                            isEmpty: \.isEmpty) { c in
                FocusTintReader { tint in chart(c, tint: tint) }
            }
        } accessory: {
            if let c = feed.value { Text("^[\(c.rows.count) day](inflect: true)") }
        }
    }

    private func dayLabel(_ r: WorkHoursChart.Row) -> String {
        r.date.formatted(.dateTime.weekday(.abbreviated).day().locale(calendar.locale ?? .current))
    }

    private func summary(_ r: WorkHoursChart.Row) -> Text {
        guard r.hasWork else { return Text(verbatim: "—").foregroundStyle(.tertiary) }
        return Text("\(Text(DurationText.minutes(r.activeSec / 60)).foregroundStyle(.primary)) \(Text("· ^[\(r.sessions) session](inflect: true)").foregroundStyle(.secondary))")
    }

    private func summaryString(_ r: WorkHoursChart.Row) -> String {
        guard r.hasWork else { return String(localized: "No work") }
        return String(AttributedString(localized: "\(DurationText.minutes(r.activeSec / 60)) active, ^[\(r.sessions) session](inflect: true)").characters)
    }

    /// Rows are numeric bands (row 0, the oldest, on top) so bars get a
    /// real height; a categorical axis would size them from its band.
    private func chart(_ c: WorkHoursChart, tint: FocusTint) -> some View {
        let n = c.rows.count
        let top = { (i: Int) in Double(n - i) }
        let rowAt = { (center: Double) -> WorkHoursChart.Row? in
            let i = n - 1 - Int(center.rounded(.down))
            return c.rows.indices.contains(i) ? c.rows[i] : nil
        }
        let inset = n > 10 ? 0.22 : 0.28
        let centers = (0..<n).map { top($0) - 0.5 }
        return Chart {
            ForEach(Array(c.rows.enumerated()), id: \.element.id) { i, r in
                if let s = r.start, let e = r.end {
                    RectangleMark(xStart: .value("Start", s), xEnd: .value("End", max(e, s + 0.15)),
                                  yStart: .value("Row", top(i) - 1 + inset), yEnd: .value("Row", top(i) - inset))
                        .foregroundStyle(r.isToday ? tint.current : tint.past)
                        .clipShape(Capsule())
                }
            }
            if let h = c.nowHour, n > 0, !tint.redacted {
                RuleMark(x: .value("Now", h), yStart: .value("Row", 0.08), yEnd: .value("Row", 0.92))
                    .foregroundStyle(.red)
                    .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round))
            }
        }
        .chartXScale(domain: c.hourDomain)
        .chartYScale(domain: 0...Double(max(n, 1)))
        .chartXAxis {
            let span = c.hourDomain.upperBound - c.hourDomain.lowerBound
            AxisMarks(values: .stride(by: span > 12 ? 3 : 2)) { value in
                AxisGridLine()
                AxisValueLabel {
                    if let h = value.as(Double.self) { hourLabel(Int(h), calendar: calendar) }
                }
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: centers) { value in
                AxisValueLabel {
                    if let v = value.as(Double.self), let r = rowAt(v) {
                        Text(verbatim: dayLabel(r))
                            .font(.caption)
                            .foregroundStyle(r.isToday ? .primary : .secondary)
                    }
                }
            }
            AxisMarks(position: .trailing, values: centers) { value in
                AxisValueLabel {
                    if let v = value.as(Double.self), let r = rowAt(v) {
                        summary(r).font(.caption).monospacedDigit()
                    }
                }
            }
        }
        .accessibilityLabel("Work hours per day")
        .accessibilityChartDescriptor(WorkHoursDescriptor(chart: c, label: dayLabel, summary: summaryString))
    }
}

// MARK: Card 9 — When you focus

struct WhenYouFocusCard: View {
    let heatmap: Loadable<Heatmap>
    var todayKey: String?
    var now = Date()
    var calendar = Calendar.current

    @State private var hoverX: Double?
    @State private var hoverY: Double?

    private struct Model: Equatable, Sendable {
        let grid: HeatmapGrid
        let strip: CalendarStrip
        var isEmpty: Bool { grid.isEmpty && strip.isEmpty }
    }

    private func model(_ h: Heatmap) -> Model {
        Model(grid: HeatmapGrid(heatmap: h, calendar: calendar),
              strip: CalendarStrip(calendar: h.calendar, today: todayKey, now: now, in: calendar))
    }

    var body: some View {
        let feed = heatmap.map(model)
        DashboardCard(title: "When you focus", systemImage: "square.grid.3x3.fill", height: DashboardCardHeight.wide) {
            focusStatsState(feed, placeholder: model(DashboardPlaceholders.heatmap), isEmpty: \.isEmpty) { m in
                FocusTintReader { tint in
                    HStack(alignment: .top, spacing: 24) {
                        VStack(alignment: .leading, spacing: 4) {
                            Text("By hour, last 12 weeks").font(.caption).foregroundStyle(.secondary)
                            hourGrid(m.grid, tint: tint)
                        }
                        VStack(alignment: .leading, spacing: 4) {
                            Text("^[\(m.strip.activeDays) active day](inflect: true)")
                                .font(.caption).foregroundStyle(.secondary)
                            calendarStrip(m.strip, tint: tint)
                        }
                        .frame(width: 190)
                    }
                }
            }
        } accessory: {
            if let grid = feed.value?.grid, let peak = grid.peak {
                Text("Peak \(grid.rowLabels[peak.row]) \(timeOfDayLabel(peak.hour, calendar: calendar))")
            }
        }
    }

    /// Cells are numeric unit squares (row 0 on top): exact gaps and
    /// heights at any card size.
    private func hourGrid(_ g: HeatmapGrid, tint: FocusTint) -> some View {
        let hovered: HeatmapGrid.Cell? = {
            guard let x = hoverX, let y = hoverY else { return nil }
            let hour = Int(x.rounded(.down)), row = 6 - Int(y.rounded(.down))
            return g.cells.first { $0.hour == hour && $0.row == row }
        }()
        return Chart {
            ForEach(g.cells) { cell in
                RectangleMark(xStart: .value("Hour", Double(cell.hour) + 0.07),
                              xEnd: .value("Hour", Double(cell.hour) + 0.93),
                              yStart: .value("Row", Double(6 - cell.row) + 0.09),
                              yEnd: .value("Row", Double(7 - cell.row) - 0.09))
                    .foregroundStyle(tint.heat(cell.minutes, max: g.maxMinutes))
                    .clipShape(RoundedRectangle(cornerRadius: 2, style: .continuous))
            }
            if let cell = hovered {
                RectangleMark(xStart: .value("Hour", Double(cell.hour)), xEnd: .value("Hour", Double(cell.hour) + 1),
                              yStart: .value("Row", Double(6 - cell.row)), yEnd: .value("Row", Double(7 - cell.row)))
                    .foregroundStyle(.clear)
                    .annotation(position: .top, overflowResolution: .init(x: .fit(to: .chart), y: .fit(to: .chart))) {
                        ChartCallout {
                            Text("\(g.rowLabels[cell.row]) \(timeOfDayLabel(cell.hour, calendar: calendar))")
                            Text("\(cell.minutes) min")
                        }
                    }
            }
        }
        .chartXScale(domain: 0...24)
        .chartYScale(domain: 0...7)
        .chartXAxis {
            AxisMarks(values: Array(stride(from: 0.5, to: 24, by: 3))) { value in
                AxisValueLabel {
                    if let v = value.as(Double.self) { hourLabel(Int(v), calendar: calendar) }
                }
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: (0..<7).map { Double($0) + 0.5 }) { value in
                AxisValueLabel {
                    if let v = value.as(Double.self) { Text(verbatim: g.rowLabels[6 - Int(v)]).font(.caption2) }
                }
            }
        }
        .chartXSelection(value: $hoverX)
        .chartYSelection(value: $hoverY)
        .accessibilityLabel("Focus minutes by weekday and hour")
        .accessibilityChartDescriptor(HeatmapDescriptor(grid: g, hourName: { hour in
            let d = calendar.date(bySettingHour: hour, minute: 0, second: 0, of: DashboardReference.day) ?? DashboardReference.day
            return d.formatted(.dateTime.hour().locale(calendar.locale ?? .current))
        }))
    }

    private func calendarStrip(_ s: CalendarStrip, tint: FocusTint) -> some View {
        Chart {
            ForEach(s.cells) { cell in
                RectangleMark(xStart: .value("Week", Double(cell.column) + 0.08),
                              xEnd: .value("Week", Double(cell.column) + 0.92),
                              yStart: .value("Row", Double(6 - cell.row) + 0.09),
                              yEnd: .value("Row", Double(7 - cell.row) - 0.09))
                    .foregroundStyle(tint.heat(cell.focusMin, max: s.maxMinutes))
                    .clipShape(RoundedRectangle(cornerRadius: 2, style: .continuous))
                    .accessibilityLabel(Text(cell.date, format: .dateTime.month().day()))
                    .accessibilityValue(Text("\(cell.focusMin) min"))
            }
        }
        .chartXScale(domain: 0...Double(s.weeks))
        .chartYScale(domain: 0...7)
        .chartXAxis {
            AxisMarks(values: monthStarts(s).map { Double($0) + 0.5 }) { value in
                AxisValueLabel {
                    if let v = value.as(Double.self),
                       let d = s.cells.first(where: { $0.column == Int(v) })?.date {
                        Text(d, format: .dateTime.month(.abbreviated))
                    }
                }
            }
        }
        .chartYAxis(.hidden)
        .accessibilityLabel("Focus per day, last 12 weeks")
    }

    /// Columns where a month starts, for the strip's axis labels.
    private func monthStarts(_ s: CalendarStrip) -> [Int] {
        var out: [Int] = []
        var lastMonth = -1
        for col in 0..<s.weeks {
            guard let d = s.cells.first(where: { $0.column == col })?.date else { continue }
            let m = calendar.component(.month, from: d)
            if m != lastMonth { out.append(col); lastMonth = m }
        }
        return out
    }
}

/// The hover/selection bubble on charts.
struct ChartCallout<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 1) { content }
            .font(.caption)
            .monospacedDigit()
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .background(Color(nsColor: .windowBackgroundColor), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(.separator))
            .shadow(color: .black.opacity(0.12), radius: 3, y: 1)
    }
}

/// An x domain covering whole days, so the last day's bar and centred
/// label aren't clipped at the plot edge.
enum DayRange {
    static func domain(_ days: [Date], calendar: Calendar) -> ClosedRange<Date> {
        guard let first = days.first, let last = days.last,
              let end = calendar.date(byAdding: .day, value: 1, to: last) else { return Date()...Date() }
        return first...end
    }

    static func ticks(_ days: [Date], calendar: Calendar) -> [Date] {
        days + [domain(days, calendar: calendar).upperBound]
    }
}
