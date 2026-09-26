#if DEBUG
import SwiftUI
import EmberKit

// One preview per card on fixture data (the Go goldens plus synthetic
// history, see DashboardFixtures), and the whole grid per scenario.

private let f = DashboardFixtures.self

private struct CardPreview<Content: View>: View {
    var width: CGFloat = 460
    @ViewBuilder let content: Content
    var body: some View { content.frame(width: width).padding(20) }
}

#Preview("Dashboard — established") {
    ScrollView { DashboardContent(source: f.established) }.frame(width: 980, height: 1800)
}
#Preview("Dashboard — new user") {
    ScrollView { DashboardContent(source: f.newUser) }.frame(width: 760, height: 2200)
}
#Preview("Dashboard — old server") {
    ScrollView { DashboardContent(source: f.oldServer) }.frame(width: 980, height: 1800)
}
#Preview("Dashboard — Pomodoro off") {
    ScrollView { DashboardContent(source: f.pomodoroOff) }.frame(width: 980, height: 1800)
}
#Preview("Dashboard — offline") {
    ScrollView { DashboardContent(source: f.offline) }.frame(width: 980, height: 1800)
}
#Preview("Dashboard — loading") {
    ScrollView { DashboardContent(source: f.loading) }.frame(width: 980, height: 1800)
}

#Preview("Clock") {
    let d = f.established
    CardPreview(width: 900) { ClockCard(screen: d.screen,
                                           actions: DashboardActions(displayPower: d.clockHealth.value?.device?.matrixPower)) }
}
#Preview("Focus — running") {
    let d = f.established
    CardPreview { FocusCard(stats: d.stats, pomodoro: d.pomodoro, config: d.pomoConfig, now: d.now) }
}
#Preview("Focus — new user") {
    let d = f.newUser
    CardPreview { FocusCard(stats: d.stats, pomodoro: d.pomodoro, config: d.pomoConfig, now: d.now) }
}
#Preview("Usage") {
    let d = f.established
    CardPreview { UsageCard(usage: d.usage, rows: d.usageRows, now: d.now) }
}
#Preview("Upcoming") {
    let d = f.established
    CardPreview { UpcomingCard(meetings: d.meetings, reminders: d.reminders, meetingsEnabled: true, now: d.now) }
}
#Preview("Agents") {
    let d = f.established
    CardPreview(width: 900) { AgentsCard(snapshot: d.snapshot, now: d.now) }
}
#Preview("Last 7 days") {
    let d = f.established
    CardPreview { LastSevenDaysCard(stats: d.stats, focusMinutes: 25, calendar: d.calendar) }
}
#Preview("12 weeks") {
    let d = f.established
    CardPreview { TwelveWeeksCard(stats: d.stats, now: d.now, calendar: d.calendar) }
}
#Preview("Work hours") {
    let d = f.established
    CardPreview(width: 900) { WorkHoursCard(workhours: d.workhours, now: d.now, calendar: d.calendar) }
}
#Preview("When you focus") {
    let d = f.established
    CardPreview(width: 900) {
        WhenYouFocusCard(heatmap: d.heatmap, todayKey: d.stats.value?.today.date, now: d.now, calendar: d.calendar)
    }
}
#Preview("Agent time") {
    let d = f.established
    CardPreview { AgentTimeCard(activity: d.activity, calendar: d.calendar) }
}
#Preview("Clock health") {
    let d = f.established
    CardPreview { ClockHealthCard(health: d.clockHealth, webURL: d.clockWebURL, now: d.now) }
}
#Preview("Weather") {
    let d = f.established
    CardPreview { WeatherCard(weather: d.weather, now: d.now) }
}
#endif
