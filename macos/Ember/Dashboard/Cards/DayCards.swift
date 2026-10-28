import Charts
import SwiftUI
import EmberKit

func clockText(hours: Double, calendar: Calendar) -> String {
    let minutes = Int((hours * 60).rounded())
    return clockText(hour: (minutes / 60) % 24, minute: minutes % 60, calendar: calendar)
}

func clockText(hour: Int, minute: Int, calendar: Calendar) -> String {
    let date = calendar.date(bySettingHour: hour, minute: minute, second: 0, of: DashboardReference.day)
        ?? DashboardReference.day
    var style = Date.FormatStyle(date: .omitted, time: .shortened)
    style.timeZone = calendar.timeZone
    return date.formatted(style.locale(calendar.locale ?? .current))
}

func clockText(hhmm: String, calendar: Calendar) -> String? {
    let parts = hhmm.split(separator: ":").compactMap { Int($0) }
    guard parts.count >= 2, (0...23).contains(parts[0]), (0..<60).contains(parts[1]) else { return nil }
    return clockText(hour: parts[0], minute: parts[1], calendar: calendar)
}

@MainActor
private func dayFeedState<T, C: View>(_ feed: Loadable<T>, placeholder: T?, isEmpty: @escaping (T) -> Bool,
                                      @ViewBuilder content: @escaping (T) -> C) -> some View {
    FeedStateView(feed: feed, placeholder: placeholder, isEmpty: isEmpty, emptyTitle: "No days on the clock yet",
                  emptySymbol: "clock.badge.questionmark",
                  offTitle: "Day clock is off", offDescription: "Turn it on in Settings › Day Clock.",
                  offSymbol: "clock.badge.xmark", offSettingsPane: .app(.dayClock), content: content)
}

private struct DayFact: View {
    let title: LocalizedStringKey
    let value: String
    var help: LocalizedStringKey?

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(verbatim: value).font(.title3).monospacedDigit()
        }
        .help(help ?? "")
        .accessibilityElement(children: .combine)
    }
}

struct DayTodayCard: View {
    let state: Loadable<DayState>
    var now = Date()
    var calendar = Calendar.current

    private func model() -> Loadable<DayState> {
        if let s = state.value, !s.enabled { return .failed(.featureOff, last: nil, lastAt: nil) }
        return state
    }

    var body: some View {
        DashboardCard(title: "Today", systemImage: "clock") {
            dayFeedState(model(), placeholder: DashboardPlaceholders.dayState, isEmpty: { _ in false }) { s in
                content(s)
            }
        }
    }

    private func phase(_ s: DayState) -> (title: LocalizedStringKey, color: Color) {
        switch s.phase {
        case .on: ("On the clock", .green)
        case .onBreak: ("On a break", .blue)
        case .off: ("Off the clock", .secondary)
        }
    }

    private func content(_ s: DayState) -> some View {
        let p = phase(s)
        let active = s.phase != .off
        let off = !active
        return VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 8) {
                Circle().fill(p.color).frame(width: 9, height: 9).accessibilityHidden(true)
                Text(p.title).font(.title2.weight(.semibold))
            }
            Grid(alignment: .leading, horizontalSpacing: 28, verticalSpacing: 10) {
                GridRow {
                    DayFact(title: "Started", value: active ? (s.started.flatMap { clockText(hhmm: $0, calendar: calendar) } ?? "—") : "—")
                    DayFact(title: "On the clock", value: active ? DurationText.minutes(s.onMin) : "—")
                }
                GridRow {
                    DayFact(title: "Stop target", value: s.stopAt.flatMap { clockText(hhmm: $0, calendar: calendar) } ?? "—")
                    DayFact(title: "Off the clock last night", value: off || s.repliesOffLastNight > 0 ? s.repliesOffLastNight.formatted() : "—",
                            help: "Replies to agents after you clocked out, last night")
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

}

struct DayWeekCard: View {
    let weeks: Loadable<DayWeeks>
    var now = Date()
    var calendar = Calendar.current

    private func model(_ w: DayWeeks) -> DayWeekTable { DayWeekTable(weeks: w.weeks, now: now, calendar: calendar) }

    var body: some View {
        let feed = weeks.map(model)
        DashboardCard(title: "This week", systemImage: "calendar") {
            dayFeedState(feed, placeholder: model(DashboardPlaceholders.dayWeeks), isEmpty: { _ in false }) { t in
                content(t.thisWeek)
            }
        }
    }

    private func content(_ w: DayWeekTable.Row?) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            if let w, w.daysWorked > 0 {
                Text("\(w.daysInRange) of \(w.daysWorked) days in range", comment: "This week's score: days stopped by the stop time, then days worked.")
                    .font(.title2.weight(.semibold))
                    .help("Days you stopped by your stop time, with a little grace")
                Grid(alignment: .leading, horizontalSpacing: 28, verticalSpacing: 10) {
                    GridRow {
                        DayFact(title: "On the clock", value: DurationText.minutes(w.onSec / 60))
                        DayFact(title: "Off the clock", value: inflected(LocalizedStringResource("^[\(w.repliesOff) reply](inflect: true)", comment: "Count of replies to agents made while off the clock; the argument is the count.")),
                                help: "Replies to agents while you were clocked out")
                    }
                }
            } else {
                Text("No days on the clock this week").font(.title3).foregroundStyle(.secondary)
                if let w, w.repliesOff > 0 {
                    DayFact(title: "Off the clock", value: inflected(LocalizedStringResource("^[\(w.repliesOff) reply](inflect: true)", comment: "Count of replies to agents made while off the clock; the argument is the count.")))
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct DayStripCard: View {
    let history: Loadable<DayHistory>
    var now = Date()
    var calendar = Calendar.current

    @State private var selectedY: Double?
    @State private var showsTable = false

    private func model(_ h: DayHistory) -> DayStrip { DayStrip(days: h.days, now: now, calendar: calendar) }

    var body: some View {
        let feed = history.map(model)
        DashboardCard(title: "Each day", systemImage: "chart.bar.xaxis", height: DashboardCardHeight.tall) {
            dayFeedState(feed, placeholder: model(DashboardPlaceholders.dayHistory), isEmpty: \.isEmpty) { strip in
                FocusTintReader { tint in
                    VStack(spacing: 6) {
                        if showsTable { table(strip) } else { chart(strip, tint: tint) }
                        legend
                    }
                }
            }
        } accessory: {
            if let strip = feed.value, !strip.isEmpty {
                Picker("View", selection: $showsTable) {
                    Image(systemName: "chart.bar.xaxis").tag(false).help("Chart")
                    Image(systemName: "tablecells").tag(true).help("Table")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
            }
        }
    }

    private func dayLabel(_ r: DayStrip.Row) -> String {
        r.date.formatted(.dateTime.weekday(.abbreviated).day().locale(calendar.locale ?? .current))
    }

    private func time(_ hours: Double?) -> String {
        hours.map { clockText(hours: $0, calendar: calendar) } ?? "—"
    }

    private func detail(_ r: DayStrip.Row) -> some View {
        ChartCallout {
            Text(r.date, format: .dateTime.weekday(.wide).month(.abbreviated).day())
            if r.hasClock {
                Text("\(time(r.start)) → \(r.stop == nil ? String(localized: "now") : time(r.stop))", comment: "Hover on a day: clock-in time, arrow, clock-out time or \"now\".")
                if let target = r.target { Text("Stop target \(time(target))", comment: "Hover on a day: the stop time set, as a clock time.") }
                Text("\(DurationText.minutes(r.onSec / 60)) on the clock · \(DurationText.minutes(r.breakSec / 60)) breaks", comment: "Hover on a day: time on the clock, then time on breaks, both durations.")
                if r.auto { Text("Stopped automatically") }
                if r.stop != nil, let inRange = r.inRange { Text(inRange ? "In range" : "Past the stop time") }
            } else {
                Text("Not clocked in")
            }
            Text("^[\(r.repliesOff) reply](inflect: true) off the clock · \(r.repliesOn) on", comment: "Hover on a day: replies to agents off the clock (counted), then replies on the clock (a number).")
        }
    }

    private func chart(_ s: DayStrip, tint: FocusTint) -> some View {
        let n = s.rows.count
        let slots = max(n, 14)
        let top = { (i: Int) in Double(slots - i) }
        let rowAt = { (y: Double) -> DayStrip.Row? in
            let i = slots - 1 - Int(y.rounded(.down))
            return s.rows.indices.contains(i) ? s.rows[i] : nil
        }
        let centers = (0..<n).map { top($0) - 0.5 }
        let selected = selectedY.flatMap(rowAt)
        return Chart {
            ForEach(Array(s.rows.enumerated()), id: \.element.id) { i, r in
                if r.isWeekend {
                    RectangleMark(xStart: .value("From", DayStrip.axis.lowerBound), xEnd: .value("To", DayStrip.axis.upperBound),
                                  yStart: .value("Row", top(i) - 1), yEnd: .value("Row", top(i)))
                        .foregroundStyle(.secondary.opacity(0.1))
                }
                ForEach(Array(r.spans.enumerated()), id: \.offset) { _, span in
                    RectangleMark(xStart: .value("Start", span.from), xEnd: .value("End", max(span.to, span.from + 0.12)),
                                  yStart: .value("Row", top(i) - 0.8), yEnd: .value("Row", top(i) - 0.2))
                        .foregroundStyle(r.isToday ? tint.current : tint.past)
                        .clipShape(Capsule())
                }
                if let target = r.target {
                    RuleMark(x: .value("Stop target", target), yStart: .value("Row", top(i) - 0.92), yEnd: .value("Row", top(i) - 0.08))
                        .foregroundStyle(.primary.opacity(tint.redacted ? 0.2 : 0.75))
                        .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round))
                }
                ForEach(Array(r.dots.enumerated()), id: \.offset) { _, h in
                    PointMark(x: .value("Reply", h), y: .value("Row", top(i) - 0.5))
                        .symbol(.circle)
                        .symbolSize(26)
                        .foregroundStyle(tint.redacted ? Color.secondary.opacity(0.3) : Color.pink)
                }
                if r.auto, let stop = r.stop {
                    PointMark(x: .value("Auto-stop", min(stop, DayStrip.axis.upperBound)), y: .value("Row", top(i) - 0.5))
                        .symbol(.diamond.strokeBorder(lineWidth: 1.5))
                        .symbolSize(44)
                        .foregroundStyle(.primary)
                }
            }
            if let selected, let i = s.rows.firstIndex(of: selected) {
                RectangleMark(xStart: .value("From", DayStrip.axis.lowerBound), xEnd: .value("To", DayStrip.axis.upperBound),
                              yStart: .value("Row", top(i) - 1), yEnd: .value("Row", top(i)))
                    .foregroundStyle(.secondary.opacity(0.15))
                    .annotation(position: .top, alignment: .center,
                                overflowResolution: .init(x: .fit(to: .plot), y: .fit(to: .chart))) { detail(selected) }
            }
        }
        .chartXScale(domain: DayStrip.axis)
        .chartYScale(domain: 0...Double(slots))
        .chartYSelection(value: $selectedY)
        .chartXAxis {
            AxisMarks(values: .stride(by: 2)) { value in
                AxisGridLine()
                AxisValueLabel { if let h = value.as(Double.self) { hourLabel(Int(h), calendar: calendar) } }
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: centers) { value in
                AxisValueLabel {
                    if let v = value.as(Double.self), let r = rowAt(v) {
                        Text(verbatim: dayLabel(r)).font(.caption2)
                            .foregroundStyle(r.isToday ? .primary : .secondary)
                    }
                }
            }
            AxisMarks(position: .trailing, values: centers) { value in
                AxisValueLabel {
                    if let v = value.as(Double.self), let r = rowAt(v) {
                        Text(verbatim: r.hasClock ? DurationText.minutes(r.onSec / 60) : "—")
                            .font(.caption2).monospacedDigit().foregroundStyle(.secondary)
                    }
                }
            }
        }
        .accessibilityLabel("Day clock, last 28 days")
        .accessibilityChartDescriptor(DayStripDescriptor(strip: s, label: dayLabel, time: { time($0) },
                                                          summary: { summaryString($0) }))
    }

    private func summaryString(_ r: DayStrip.Row) -> String {
        guard r.hasClock else { return inflected(LocalizedStringResource("Not clocked in, ^[\(r.repliesOff) reply](inflect: true) off the clock", comment: "VoiceOver summary of a day without a clock-in; the argument is the off-clock reply count.")) }
        return inflected(LocalizedStringResource("\(DurationText.minutes(r.onSec / 60)) on the clock, ^[\(r.repliesOff) reply](inflect: true) off the clock", comment: "VoiceOver summary of a day: time on the clock (a duration), then the off-clock reply count."))
    }

    private func table(_ s: DayStrip) -> some View {
        Table(Array(s.rows.reversed())) {
            TableColumn("Day") { r in Text(verbatim: dayLabel(r)) }
            TableColumn("Started") { r in Text(verbatim: time(r.start)).monospacedDigit() }
            TableColumn("Stopped") { r in
                Text(verbatim: time(r.stop) + (r.auto ? " ◇" : "")).monospacedDigit()
                    .accessibilityLabel(r.auto ? Text("\(time(r.stop)), stopped automatically", comment: "VoiceOver label of a table cell: the clock-out time of a day that ended automatically.") : Text(verbatim: time(r.stop)))
            }
            TableColumn("Stop target") { r in Text(verbatim: time(r.target)).monospacedDigit() }
            TableColumn("On the clock") { r in Text(verbatim: r.hasClock ? DurationText.minutes(r.onSec / 60) : "—").monospacedDigit() }
            TableColumn("Breaks") { r in Text(verbatim: r.hasClock ? DurationText.minutes(r.breakSec / 60) : "—").monospacedDigit() }
            TableColumn("Off-clock replies") { r in Text(verbatim: r.repliesOff.formatted()).monospacedDigit() }
            TableColumn("In range") { r in
                Text(r.stop != nil ? (r.inRange.map { $0 ? "Yes" : "No" } ?? "—") : "—")
            }
        }
    }

    private var legend: some View {
        HStack(spacing: 14) {
            legendItem("On the clock") { Capsule().fill(Color.accentColor).frame(width: 16, height: 6) }
            legendItem("Stop target") { Capsule().fill(.primary.opacity(0.75)).frame(width: 2, height: 11) }
            legendItem("Reply off the clock") { Circle().fill(Color.pink).frame(width: 6, height: 6) }
            legendItem("Stopped automatically") { Image(systemName: "diamond").font(.system(size: 8, weight: .bold)) }
            Spacer(minLength: 0)
        }
        .font(.caption2)
        .foregroundStyle(.secondary)
    }

    private func legendItem<M: View>(_ title: LocalizedStringKey, @ViewBuilder mark: () -> M) -> some View {
        HStack(spacing: 4) { mark().accessibilityHidden(true); Text(title) }
    }
}

struct StopTrendCard: View {
    let history: Loadable<DayHistory>
    var now = Date()
    var calendar = Calendar.current

    @State private var selected: Date?

    private func model(_ h: DayHistory) -> StopTrend {
        StopTrend(strip: DayStrip(days: h.days, now: now, calendar: calendar))
    }

    var body: some View {
        let feed = history.map(model)
        DashboardCard(title: "Stop time", systemImage: "chart.line.downtrend.xyaxis") {
            FeedStateView(feed: feed, placeholder: model(DashboardPlaceholders.dayHistory), isEmpty: \.isEmpty,
                          emptyTitle: "No stops yet", emptySymbol: "chart.line.downtrend.xyaxis",
                          offTitle: "Day clock is off", offDescription: "Turn it on in Settings › Day Clock.",
                          offSymbol: "clock.badge.xmark", offSettingsPane: .app(.dayClock)) { t in
                FocusTintReader { tint in chart(t, tint: tint) }
            }
        } accessory: {
            if let avg = feed.value?.averageMinutesPastTarget {
                if avg == 0 {
                    Text("on target on average")
                } else if avg > 0 {
                    Text("avg \(DurationText.minutes(avg)) past target", comment: "Stop time card: average distance past the stop target; the argument is a duration.")
                } else {
                    Text("avg \(DurationText.minutes(-avg)) before target", comment: "Stop time card: average distance before the stop target; the argument is a duration.")
                }
            }
        }
    }

    private func chart(_ t: StopTrend, tint: FocusTint) -> some View {
        let nearest = selected.flatMap { sel in
            t.points.min { abs($0.date.timeIntervalSince(sel)) < abs($1.date.timeIntervalSince(sel)) }
        }
        let span = t.hourDomain.upperBound - t.hourDomain.lowerBound
        return Chart {
            ForEach(t.points) { p in
                if let target = p.target {
                    LineMark(x: .value("Day", p.date, unit: .day), y: .value("Stop target", target),
                             series: .value("Series", "Target"))
                        .interpolationMethod(.stepCenter)
                        .foregroundStyle(.secondary)
                        .lineStyle(StrokeStyle(lineWidth: 1.5, dash: [4, 3]))
                }
            }
            ForEach(t.points) { p in
                LineMark(x: .value("Day", p.date, unit: .day), y: .value("Stopped", p.stop),
                         series: .value("Series", "Stop"))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(tint.current)
                    .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round))
                if p.auto {
                    PointMark(x: .value("Day", p.date, unit: .day), y: .value("Stopped", p.stop))
                        .symbol(.diamond.strokeBorder(lineWidth: 1.5))
                        .symbolSize(44)
                        .foregroundStyle(tint.current)
                } else {
                    PointMark(x: .value("Day", p.date, unit: .day), y: .value("Stopped", p.stop))
                        .symbol(.circle)
                        .symbolSize(28)
                        .foregroundStyle(tint.current)
                }
            }
            if let p = nearest {
                RuleMark(x: .value("Day", p.date, unit: .day))
                    .foregroundStyle(.secondary.opacity(0.4))
                    .annotation(position: .top, overflowResolution: .init(x: .fit(to: .plot), y: .fit(to: .chart))) {
                        ChartCallout {
                            Text(p.date, format: .dateTime.weekday(.wide).month(.abbreviated).day())
                            Text("Stopped \(clockText(hours: p.stop, calendar: calendar))", comment: "Hover on a stop-time point: when the day ended, as a clock time.")
                            if let target = p.target { Text("Target \(clockText(hours: target, calendar: calendar))", comment: "Hover on a stop-time point: the stop target, as a clock time.") }
                            if p.auto { Text("Stopped automatically") }
                        }
                    }
            }
        }
        .chartYScale(domain: t.hourDomain)
        .chartXSelection(value: $selected)
        .chartXAxis {
            AxisMarks(values: .stride(by: .day, count: 7)) { _ in
                AxisGridLine()
                AxisValueLabel(format: .dateTime.month(.abbreviated).day())
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: .stride(by: span > 6 ? 2 : 1)) { value in
                AxisGridLine()
                AxisValueLabel { if let h = value.as(Double.self) { hourLabel(Int(h), calendar: calendar) } }
            }
        }
        .accessibilityLabel("Stop time against the stop target")
        .accessibilityChartDescriptor(StopTrendDescriptor(trend: t, calendar: calendar))
    }
}

struct DayWeeksCard: View {
    let weeks: Loadable<DayWeeks>
    var now = Date()
    var calendar = Calendar.current

    private func model(_ w: DayWeeks) -> DayWeekTable { DayWeekTable(weeks: w.weeks, now: now, calendar: calendar) }

    var body: some View {
        let feed = weeks.map(model)
        DashboardCard(title: "Weeks", systemImage: "tablecells", height: DashboardCardHeight.wide) {
            dayFeedState(feed, placeholder: model(DashboardPlaceholders.dayWeeks), isEmpty: \.isEmpty) { t in
                table(t)
            }
        }
    }

    private func table(_ t: DayWeekTable) -> some View {
        Grid(alignment: .trailing, horizontalSpacing: 20, verticalSpacing: 4) {
            GridRow {
                Text("Week of").gridColumnAlignment(.leading)
                Text("Days worked")
                Text("Days in range")
                Text("On the clock")
                if t.hasBreaks { Text("Breaks") }
                Text("Replies off the clock")
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            Divider()
            ForEach(t.rows) { r in
                GridRow {
                    Text(r.weekStart, format: .dateTime.month(.abbreviated).day())
                        .fontWeight(r.isCurrent ? .semibold : .regular)
                    Text(r.daysWorked.formatted())
                    Text(r.daysWorked == 0 ? "—" : r.daysInRange.formatted())
                    Text(verbatim: r.daysWorked == 0 ? "—" : DurationText.minutes(r.onSec / 60))
                    if t.hasBreaks { Text(verbatim: r.breakSec.map { DurationText.minutes($0 / 60) } ?? "—") }
                    Text(r.repliesOff.formatted())
                }
                .accessibilityElement(children: .combine)
            }
        }
        .font(.callout)
        .monospacedDigit()
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }
}
