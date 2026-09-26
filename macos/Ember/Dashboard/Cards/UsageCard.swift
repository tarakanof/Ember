import SwiftUI
import EmberKit

/// Card 3: subscription usage per tool, 5-hour and 7-day windows.
struct UsageCard: View {
    let usage: Loadable<UsageSnapshot>
    let rows: [UsageRow]
    var now = Date()

    var body: some View {
        DashboardCard(title: "Usage", systemImage: "gauge.with.dots.needle.33percent") {
            FeedStateView(feed: feed, placeholder: Self.placeholder, isEmpty: \.isEmpty,
                          emptyTitle: "No usage reported yet — agents report it as they run",
                          emptySymbol: "gauge.with.dots.needle.0percent") { rows in
                VStack(alignment: .leading, spacing: 12) {
                    ForEach(rows.prefix(2)) { row in
                        ToolUsageView(row: row, now: now)
                        if row.id != rows.prefix(2).last?.id { Divider() }
                    }
                }
            }
        }
    }

    /// On a server without `GET /v1/usage` the rows come from sessions, so
    /// the off state never shows: old servers still get the 5-hour window.
    /// Session rows also show while the snapshot hasn't arrived yet.
    private var feed: Loadable<[UsageRow]> {
        if usage.error == .featureOff || (usage.value == nil && !rows.isEmpty) { return .loaded(rows, at: now) }
        return usage.map { _ in rows }
    }

    static let placeholder: [UsageRow] = [UsageRow.placeholder("claude"), UsageRow.placeholder("codex")]
}

private struct ToolUsageView: View {
    let row: UsageRow
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(AppNames.display(row.tool)).font(.body.weight(.semibold))
                if let source = row.source {
                    Text(verbatim: source).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                if row.stale {
                    Label("Stale", systemImage: "clock.badge.exclamationmark")
                        .labelStyle(.iconOnly)
                        .foregroundStyle(.secondary)
                        .help("Not updated recently")
                }
                if !row.models.isEmpty {
                    Text(modelsText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            if let w = row.fiveHour { window("5h", spoken: "5-hour window", w) }
            if let w = row.sevenDay { window("7d", spoken: "7-day window", w) }
        }
    }

    private var modelsText: String {
        row.models.prefix(3).map { "\(String(localized: AppNames.display($0.name))) \(Percent.text($0.percent))" }
            .joined(separator: " · ")
    }

    private func window(_ label: LocalizedStringKey, spoken: LocalizedStringKey, _ w: UsageRow.Window) -> some View {
        HStack(spacing: 8) {
            Text(label)
                .font(.caption.weight(.medium))
                .foregroundStyle(.secondary)
                .frame(minWidth: 20, alignment: .leading)
                .fixedSize()
            Gauge(value: min(max(w.percent, 0), 100), in: 0...100) { Text(label) }
                .gaugeStyle(.accessoryLinearCapacity)
                .tint(tint(w.percent))
                .labelsHidden()
            Text(Percent.text(w.percent))
                .font(.callout)
                .monospacedDigit()
                .frame(minWidth: 36, alignment: .trailing)
            reset(w)
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(minWidth: 52, alignment: .trailing)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text("\(Text(AppNames.display(row.tool))), \(Text(spoken))"))
        .accessibilityValue(accessibilityValue(w))
    }

    @ViewBuilder
    private func reset(_ w: UsageRow.Window) -> some View {
        if let at = w.resetsAt, at > now {
            Text("\(Image(systemName: "arrow.clockwise")) \(at, format: .dateTime.hour().minute())")
                .help(Text("Resets \(at, style: .relative)"))
        } else if let label = w.resetLabel {
            Text("\(Image(systemName: "arrow.clockwise")) \(label)")
        } else {
            Text(verbatim: "")
        }
    }

    /// "47%, resets at 16:20": the percent plus what the reset column shows.
    private func accessibilityValue(_ w: UsageRow.Window) -> Text {
        let percent = Percent.text(w.percent)
        if let at = w.resetsAt, at > now {
            return Text("\(percent), resets at \(at, format: .dateTime.hour().minute())")
        }
        if let label = w.resetLabel { return Text("\(percent), resets \(label)") }
        return Text(verbatim: percent)
    }

    private func tint(_ p: Double) -> Color {
        p >= 90 ? .red : p >= 70 ? .orange : .accentColor
    }
}

private extension UsageRow {
    static func placeholder(_ tool: String) -> UsageRow {
        UsageRow.rows(fromSessions: [try! JSONDecoder().decode(Session.self, from: Data(
            #"{"tool":"\#(tool)","state":"idle","rate_window_pct":40,"rate_reset_at":4102444800}"#.utf8))],
            now: .distantPast)[0]
    }
}
