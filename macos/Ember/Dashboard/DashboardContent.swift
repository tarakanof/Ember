import SwiftUI
import EmberKit

/// The card grid (design §2.2–2.3): the ordered card list, hidden cards
/// dropped, cut into runs for the current column count.
struct DashboardContent: View {
    let data: DashboardData
    var actions = DashboardActions()

    @State private var columns = 2

    enum CardID: Hashable, Sendable {
        case clock, focus, usage, upcoming, agents, lastSeven, twelveWeeks, workHours, heatmap
        case agentTime, clockHealth, weather
    }

    private var cards: [(id: CardID, size: CardSize)] {
        var out: [(id: CardID, size: CardSize)] = [(.clock, .wide), (.focus, .standard)]
        if data.showsUsage { out.append((.usage, .standard)) }
        if data.showsUpcoming { out.append((.upcoming, .standard)) }
        out += [(.agents, .wide), (.lastSeven, .standard), (.twelveWeeks, .standard),
                (.workHours, .wide), (.heatmap, .wide), (.agentTime, .standard), (.clockHealth, .standard)]
        if data.showsWeather { out.append((.weather, .standard)) }
        return out
    }

    var body: some View {
        VStack(spacing: Self.spacing) {
            ForEach(Array(DashboardLayout.runs(cards, columns: columns).enumerated()), id: \.offset) { _, run in
                switch run {
                case .wide(let id):
                    card(id)
                case .grid(let ids):
                    LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: Self.spacing, alignment: .top),
                                             count: columns),
                              spacing: Self.spacing) {
                        ForEach(ids, id: \.self) { card($0) }
                    }
                }
            }
        }
        .padding(20)
        .onGeometryChange(for: Int.self) { DashboardLayout.columns(forWidth: $0.size.width) } action: { columns = $0 }
    }

    static let spacing: CGFloat = 16

    @ViewBuilder
    private func card(_ id: CardID) -> some View {
        switch id {
        case .clock:
            ClockCard(screen: data.screen, health: data.clockHealth, actions: actions)
        case .focus:
            FocusCard(stats: data.stats, pomodoro: data.pomodoro, config: data.pomoConfig, now: data.now)
        case .usage:
            UsageCard(usage: data.usage, rows: data.usageRows, now: data.now)
        case .upcoming:
            UpcomingCard(meetings: data.meetings, reminders: data.reminders,
                         meetingsEnabled: data.meetingsEnabled, now: data.now)
        case .agents:
            AgentsCard(snapshot: data.snapshot, now: data.now)
        case .lastSeven:
            LastSevenDaysCard(stats: data.stats, focusMinutes: data.focusMinutes, calendar: data.calendar)
        case .twelveWeeks:
            TwelveWeeksCard(stats: data.stats, now: data.now, calendar: data.calendar)
        case .workHours:
            WorkHoursCard(workhours: data.workhours, now: data.now, calendar: data.calendar)
        case .heatmap:
            WhenYouFocusCard(heatmap: data.heatmap, todayKey: data.stats.value?.today.date,
                             now: data.now, calendar: data.calendar)
        case .agentTime:
            AgentTimeCard(activity: data.activity, calendar: data.calendar)
        case .clockHealth:
            ClockHealthCard(health: data.clockHealth, webURL: data.clockWebURL, now: data.now,
                            openURL: actions.openURL)
        case .weather:
            WeatherCard(weather: data.weather, now: data.now)
        }
    }
}
