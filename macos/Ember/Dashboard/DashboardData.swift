import Foundation
import EmberKit

/// Everything the Dashboard's cards read, as plain values. `DashboardWindow`
/// fills it from `LiveModel`; previews and snapshot renders fill it from
/// fixtures, so no card depends on `AppEnvironment`.
struct DashboardData {
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
    /// Due-timed Apple reminders (the app watches them locally).
    var reminders: [UpcomingItem] = []
    var remindersEnabled = false
    /// nil until the Pomodoro config has loaded.
    var pomoConfig: PomoConfig?
    /// nil until the meetings config has loaded.
    var meetingsEnabled: Bool?
    /// The clock's own web UI, when the server knows its address.
    var clockWebURL: URL?
    /// The render's clock: `Date()` in the window, fixed in fixtures.
    var now = Date()
    var calendar = Calendar.current

    /// Card 12 is hidden while weather is off.
    var showsWeather: Bool { weather.value?.enabled != false }
    /// Card 4 is hidden while both meetings and reminders are off.
    var showsUpcoming: Bool { meetingsEnabled != false || remindersEnabled }
    /// Card 3 is hidden until some tool has reported usage.
    var showsUsage: Bool { !usageRows.isEmpty || usage.isLoading }

    /// Usage from `GET /v1/usage`, or from `/state` sessions on a server
    /// without it.
    var usageRows: [UsageRow] {
        if let snap = usage.value, usage.error != .featureOff { return UsageRow.rows(from: snap) }
        return UsageRow.rows(fromSessions: snapshot.value?.sessions ?? [])
    }

    /// Focus length for the goal line; the stats payload doesn't carry it.
    var focusMinutes: Int? { pomoConfig?.focusMinutes }
}

/// What the cards can ask the app to do.
struct DashboardActions {
    var clock: (ClockAction) -> Void = { _ in }
    /// Clock actions in flight, to disable their buttons.
    var running: Set<EmberAction> = []
    var openURL: (URL) -> Void = { _ in }
}
