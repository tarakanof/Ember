import Foundation
import SwiftUI
import EmberKit

/// Everything the Dashboard's cards read. Each card reads only its own
/// properties, so with `LiveDashboardSource` (which forwards to the
/// observable `LiveModel`) a feed change re-renders only the cards that show
/// it: the 1 s mirror never rebuilds the heatmap. `DashboardData` is the
/// plain-value version for previews and snapshot renders.
@MainActor
protocol DashboardSource {
    var connection: ConnectionHealth { get }
    var snapshot: Loadable<Snapshot> { get }
    var pomodoro: Loadable<PomoState> { get }
    var stats: Loadable<PomoStats> { get }
    var usage: Loadable<UsageSnapshot> { get }
    var meetings: Loadable<MeetingsState> { get }
    var screen: Loadable<[Int]> { get }
    var clockHealth: Loadable<ClockHealth> { get }
    var weather: Loadable<WeatherState> { get }
    var activity: Loadable<ActivitySummary> { get }
    var workhours: Loadable<WorkHours> { get }
    var heatmap: Loadable<Heatmap> { get }
    /// Due-timed Apple reminders (the app watches them locally).
    var reminders: [UpcomingItem] { get }
    var remindersEnabled: Bool { get }
    /// nil until the Pomodoro config has loaded.
    var pomoConfig: PomoConfig? { get }
    /// nil until the meetings config has loaded.
    var meetingsEnabled: Bool? { get }
    /// The clock's own web UI, when the server knows its address.
    var clockWebURL: URL? { get }
    var calendar: Calendar { get }
    /// A fixed clock for fixtures; nil means live (cards that show time
    /// relative to now tick on a `TimelineView`).
    var fixedNow: Date? { get }
    var actions: DashboardActions { get }
}

extension DashboardSource {
    /// Card 12 is hidden while weather is off.
    var showsWeather: Bool { weather.value?.enabled != false }
    /// Card 4 is hidden while both meetings and reminders are off.
    var showsUpcoming: Bool { meetingsEnabled != false || remindersEnabled }
    /// Card 3 is hidden until some tool has reported usage.
    var showsUsage: Bool { usage.isLoading || !usageRows.isEmpty }

    /// Usage from `GET /v1/usage`, or from `/state` sessions on a server
    /// without it.
    var usageRows: [UsageRow] {
        if let snap = usage.value, usage.error != .featureOff { return UsageRow.rows(from: snap) }
        return UsageRow.rows(fromSessions: snapshot.value?.sessions ?? [])
    }

    /// Focus length for the goal line; the stats payload doesn't carry it.
    var focusMinutes: Int? { pomoConfig?.focusMinutes }

    /// The server is offline and no card has ever loaded: one window-level
    /// message instead of a dozen identical cards. Reads the connection
    /// first, so while online no feed is touched.
    var isOfflineWithNothingLoaded: Bool {
        guard case .offline = connection else { return false }
        let feeds: [(error: FeedError?, hasValue: Bool)] = [
            (snapshot.error, snapshot.value != nil), (pomodoro.error, pomodoro.value != nil),
            (stats.error, stats.value != nil), (clockHealth.error, clockHealth.value != nil),
        ]
        return feeds.allSatisfy { !$0.hasValue && ($0.error == .offline || $0.error == nil) }
    }
}

/// Plain values: previews, fixtures, snapshot renders.
struct DashboardData: DashboardSource {
    var connection: ConnectionHealth = .online(since: .distantPast)
    var snapshot: Loadable<Snapshot> = .loading
    var pomodoro: Loadable<PomoState> = .loading
    var stats: Loadable<PomoStats> = .loading
    var usage: Loadable<UsageSnapshot> = .loading
    var meetings: Loadable<MeetingsState> = .loading
    var screen: Loadable<[Int]> = .loading
    var clockHealth: Loadable<ClockHealth> = .loading
    var weather: Loadable<WeatherState> = .loading
    var activity: Loadable<ActivitySummary> = .loading
    var workhours: Loadable<WorkHours> = .loading
    var heatmap: Loadable<Heatmap> = .loading
    var reminders: [UpcomingItem] = []
    var remindersEnabled = false
    var pomoConfig: PomoConfig?
    var meetingsEnabled: Bool?
    var clockWebURL: URL?
    var now = Date()
    var calendar = Calendar.current
    var actions = DashboardActions()
    var fixedNow: Date? { now }
}

/// What the cards can ask the app to do.
struct DashboardActions {
    var clock: (ClockAction) -> Void = { _ in }
    /// Clock actions in flight, to disable their buttons.
    var running: Set<EmberAction> = []
}

/// Copy shared by the cards whose routes arrived with the dashboard API.
enum ServerRequirement {
    /// The first server version with the Dashboard read routes.
    static let dashboardVersion = "0.28"
    static var title: LocalizedStringKey { "Needs server \(dashboardVersion)" }
    static let symbol = "arrow.up.circle"
}
