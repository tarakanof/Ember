import SwiftUI
import EmberKit

/// Card 5: every agent session the server knows, what needs attention first.
struct AgentsCard: View {
    let snapshot: Loadable<Snapshot>
    var now = Date()

    /// A wide card sits in its own row, so it can hug a short table instead
    /// of showing a mostly empty one; it grows to the standard wide height.
    private func height(_ table: Loadable<AgentsTable>) -> CGFloat {
        guard let n = (table.value ?? (table.isLoading ? Self.placeholder : nil))?.rows.count, n > 0 else {
            return DashboardCardHeight.standard
        }
        return min(DashboardCardHeight.wide, 96 + CGFloat(n) * 28)
    }

    private static let placeholder = AgentsTable(sessions: DashboardPlaceholders.snapshot.sessions)

    var body: some View {
        let table = snapshot.map { AgentsTable(sessions: $0.sessions) }
        DashboardCard(title: "Agents", systemImage: "sparkles", height: height(table)) {
            FeedStateView(feed: table, placeholder: Self.placeholder, isEmpty: \.isEmpty,
                          emptyTitle: "No active sessions", emptySymbol: "sparkles") { t in
                SessionsTable(rows: t.rows, now: now)
            }
        } accessory: {
            if let t = table.value, !t.isEmpty {
                Text("\(t.running) running · \(t.waiting) waiting")
            }
        }
    }
}

private struct SessionsTable: View {
    let rows: [AgentsTable.Row]
    let now: Date

    var body: some View {
        Table(rows) {
            TableColumn("Source") { r in
                HStack(spacing: 6) {
                    if let hex = r.session.sourceColor {
                        Circle().fill(EmberColors.hex(hex, fallback: .secondary)).frame(width: 6, height: 6)
                            .accessibilityHidden(true)
                    }
                    Text(verbatim: r.session.source.isEmpty ? "—" : r.session.source)
                }
            }
            .width(min: 44, ideal: 56, max: 110)
            TableColumn("Tool") { r in Text(AppNames.display(r.session.tool)) }
                .width(min: 48, ideal: 58, max: 90)
            TableColumn("State") { r in PhaseBadge(state: r.session.stateEnum) }
                .width(min: 70, ideal: 80, max: 100)
            TableColumn("Activity") { r in
                Text(verbatim: SessionPresentation(r.session).subtitle(maxLength: 120) ?? "—")
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(SessionPresentation(r.session).subtitle(maxLength: 400) ?? "")
            }
            .width(min: 90, ideal: 170)
            TableColumn("Context") { r in ContextCell(pct: r.session.contextPct) }
                .width(min: 70, ideal: 82, max: 120)
            TableColumn("5h") { r in
                Text(r.session.rateWindowPct.map { Percent.text(Double($0)) } ?? "—")
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
            }
            .width(min: 34, ideal: 40, max: 56)
            TableColumn("Updated") { r in
                Text(RelativeText.short(r.session.updatedAt, now: now))
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            }
            .width(min: 60, ideal: 76, max: 100)
        }
        .tableStyle(.inset(alternatesRowBackgrounds: false))
        .scrollContentBackground(.hidden)
        .environment(\.defaultMinListRowHeight, 24)
        .accessibilityLabel("Agent sessions")
    }
}

private struct ContextCell: View {
    let pct: Int?

    var body: some View {
        if let pct {
            HStack(spacing: 6) {
                Gauge(value: Double(min(max(pct, 0), 100)), in: 0...100) { Text("Context") }
                    .gaugeStyle(.accessoryLinearCapacity)
                    .tint(pct >= 80 ? .orange : .accentColor)
                    .labelsHidden()
                    .frame(width: 36)
                Text(Percent.text(Double(pct))).monospacedDigit()
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel("Context")
            .accessibilityValue(Percent.text(Double(pct)))
        } else {
            Text(verbatim: "—").foregroundStyle(.secondary)
        }
    }
}

/// "just now", "2 min", "1 h": short relative times for dense rows.
enum RelativeText {
    static func short(_ date: Date, now: Date) -> String {
        let s = max(0, now.timeIntervalSince(date))
        if s < 60 { return String(localized: "just now") }
        return Duration.seconds(s).formatted(
            .units(allowed: [.days, .hours, .minutes], width: .abbreviated, maximumUnitCount: 1))
    }
}
