import SwiftUI
import EmberKit

/// The Dashboard's cards (design §2.3).
enum DashboardCardID: Hashable, Sendable, CaseIterable {
    case clock, focus, usage, upcoming, agents, lastSeven, twelveWeeks, workHours, heatmap
    case agentTime, clockHealth, weather

    /// How often a card that shows time relative to now re-renders on its own;
    /// nil for cards that only change with their feed.
    var clockTick: TimeInterval? {
        switch self {
        case .usage, .upcoming, .agents, .workHours, .clockHealth, .weather: 60
        // Only the day or week boundary matters.
        case .twelveWeeks, .heatmap: 600
        case .clock, .focus, .lastSeven, .agentTime: nil
        }
    }
}

/// The card grid (design §2.2): the ordered card list, hidden cards
/// dropped, cut into runs for the current column count. Each card is its own
/// view reading only its own feeds from `source`.
struct DashboardContent<Source: DashboardSource>: View {
    let source: Source
    var onRetry: () -> Void = {}

    @State private var columns = 2
    @Environment(\.openWindow) private var openWindow

    private var cards: [(id: DashboardCardID, size: CardSize)] {
        var out: [(id: DashboardCardID, size: CardSize)] = [(.clock, .wide), (.focus, .standard)]
        if source.showsUsage { out.append((.usage, .standard)) }
        if source.showsUpcoming { out.append((.upcoming, .standard)) }
        out += [(.agents, .wide), (.lastSeven, .standard), (.twelveWeeks, .standard),
                (.workHours, .wide), (.heatmap, .wide), (.agentTime, .standard), (.clockHealth, .standard)]
        if source.showsWeather { out.append((.weather, .standard)) }
        return out
    }

    var body: some View {
        if source.isOfflineWithNothingLoaded {
            ContentUnavailableView {
                Label("Server unreachable", systemImage: "network.slash")
            } description: {
                Text("Ember can't reach the server. Check that it's running, or change its address in Connection settings.")
            } actions: {
                Button("Try Again", action: onRetry)
                Button("Open Connection Settings") { openSettings(pane: "connection", using: openWindow) }
            }
            .frame(maxWidth: .infinity, minHeight: 480)
        } else {
            grid
        }
    }

    private var grid: some View {
        VStack(spacing: Self.spacing) {
            ForEach(Array(DashboardLayout.runs(cards, columns: columns).enumerated()), id: \.offset) { _, run in
                switch run {
                case .wide(let id):
                    DashboardCardSlot(id: id, source: source)
                case .grid(let ids):
                    LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: Self.spacing, alignment: .top),
                                             count: columns),
                              spacing: Self.spacing) {
                        ForEach(ids, id: \.self) { DashboardCardSlot(id: $0, source: source) }
                    }
                }
            }
        }
        .padding(20)
        .onGeometryChange(for: Int.self) { DashboardLayout.columns(forWidth: $0.size.width) } action: { columns = $0 }
    }

    static var spacing: CGFloat { 16 }
}

/// One card, as its own view: its body reads only that card's feeds, so a
/// change elsewhere doesn't re-render it. Its inputs (the id and an
/// `Equatable` source) compare equal across parent re-renders, so SwiftUI
/// skips it then too.
struct DashboardCardSlot<Source: DashboardSource>: View {
    let id: DashboardCardID
    let source: Source

    var body: some View {
        #if DEBUG
        let _ = DashboardRenderCounter.shared.count(id)
        #endif
        if let fixed = source.fixedNow {
            card(now: fixed)
        } else if let tick = id.clockTick {
            TimelineView(.periodic(from: .now, by: tick)) { ctx in card(now: ctx.date) }
        } else {
            card(now: Date())
        }
    }

    @ViewBuilder
    private func card(now: Date) -> some View {
        let s = source
        switch id {
        case .clock:
            ClockCard(screen: s.screen, health: s.clockHealth, actions: s.actions)
        case .focus:
            FocusCard(stats: s.stats, pomodoro: s.pomodoro, config: s.pomoConfig, now: now)
        case .usage:
            UsageCard(usage: s.usage, rows: s.usageRows, now: now)
        case .upcoming:
            UpcomingCard(meetings: s.meetings, reminders: s.reminders, meetingsEnabled: s.meetingsEnabled, now: now)
        case .agents:
            AgentsCard(snapshot: s.snapshot, now: now)
        case .lastSeven:
            LastSevenDaysCard(stats: s.stats, focusMinutes: s.focusMinutes, calendar: s.calendar)
        case .twelveWeeks:
            TwelveWeeksCard(stats: s.stats, now: now, calendar: s.calendar)
        case .workHours:
            WorkHoursCard(workhours: s.workhours, now: now, calendar: s.calendar)
        case .heatmap:
            WhenYouFocusCard(heatmap: s.heatmap, todayKey: s.stats.value?.today.date, now: now, calendar: s.calendar)
        case .agentTime:
            AgentTimeCard(activity: s.activity, calendar: s.calendar)
        case .clockHealth:
            ClockHealthCard(health: s.clockHealth, webURL: s.clockWebURL, now: now)
        case .weather:
            WeatherCard(weather: s.weather, now: now)
        }
    }
}

#if DEBUG
/// Counts card body evaluations, to check that a fast feed re-renders only
/// its own card (the snapshot tool prints it).
@MainActor
final class DashboardRenderCounter {
    static let shared = DashboardRenderCounter()
    private(set) var counts: [DashboardCardID: Int] = [:]
    func count(_ id: DashboardCardID) { counts[id, default: 0] += 1 }
    func reset() { counts = [:] }
}
#endif
