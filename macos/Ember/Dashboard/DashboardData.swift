import Foundation
import SwiftUI
import EmberKit

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
    var reminders: [UpcomingItem] { get }
    var remindersEnabled: Bool { get }
    var pomoConfig: PomoConfig? { get }
    var meetingsEnabled: Bool? { get }
    var calendar: Calendar { get }
    var fixedNow: Date? { get }
    var actions: DashboardActions { get }
}

extension DashboardSource {
    var showsWeather: Bool { weather.value?.enabled != false }
    var showsUpcoming: Bool { meetingsEnabled != false || remindersEnabled }
    var showsUsage: Bool { usage.isLoading || !usageRows.isEmpty }

    var usageRows: [UsageRow] {
        let snap = usage.error == .featureOff ? nil : usage.value
        return UsageRow.rows(from: snap, sessions: MenuRows.liveSessions(snapshot), now: fixedNow ?? Date())
    }

    var focusMinutes: Int? { pomoConfig?.focusMinutes }

    var isOfflineWithNothingLoaded: Bool {
        guard case .offline = connection else { return false }
        let feeds: [(error: FeedError?, hasValue: Bool)] = [
            (snapshot.error, snapshot.value != nil), (pomodoro.error, pomodoro.value != nil),
            (stats.error, stats.value != nil), (clockHealth.error, clockHealth.value != nil),
        ]
        return feeds.allSatisfy { !$0.hasValue && ($0.error?.isUnreachable ?? true) }
    }
}

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
    var now = Date()
    var calendar = Calendar.current
    var actions = DashboardActions()
    var fixedNow: Date? { now }
}

struct DashboardActions {
    var clock: (ClockAction) -> Void = { _ in }
    var running: Set<EmberAction> = []
    var displayPower: Bool?
    var pendingDisplayPower: Bool?
}

enum ServerRequirement {
    static let dashboardVersion = "0.28"
    static var title: LocalizedStringKey { "Needs server \(dashboardVersion)" }
    static let symbol = "arrow.up.circle"
}
