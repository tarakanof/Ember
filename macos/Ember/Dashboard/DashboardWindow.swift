import AppKit
import SwiftUI
import EmberKit

struct DashboardWindow: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openURL) private var openURL
    @Environment(\.openWindow) private var openWindow

    @State private var isVisible = true

    static let heldFeeds: [Feed] = [.stats, .usage, .meetings, .clockHealth, .screen, .activity,
                                    .workhours, .heatmap, .weather]

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Ember")
                .navigationSubtitle(Text(env.live.connection.subtitle(
                    serverHost: env.serverURL?.host(), serverVersion: env.live.serverVersion,
                    offlineReason: env.live.snapshot.error)))
                .toolbar { toolbar }
        }
        .frame(minWidth: 720, minHeight: 560)
        .background(WindowVisibilityReader(isVisible: $isVisible))
        .task(id: isVisible) {
            guard isVisible else { return }
            await env.live.track(Self.heldFeeds)
        }
        .task(id: env.serverURL) { await loadConfigs() }
        .onReceive(NotificationCenter.default.publisher(for: .emberRefreshRequested)) { _ in
            Task { await loadConfigs() }
        }
    }

    @ViewBuilder
    private var content: some View {
        if env.live.connection == .unconfigured {
            ContentUnavailableView {
                Label("Ember isn't connected to a server", systemImage: "network.slash")
            } description: {
                Text("Enter the server's address in Connection settings, or pick one Ember found on your network.")
            } actions: {
                Button("Open Connection Settings") { openSettings(.app(.connection), using: openWindow) }
            }
        } else {
            ScrollView {
                DashboardContent(source: LiveDashboardSource(env: env),
                                 onRetry: refresh)
            }
            .overlay(alignment: .bottom) { ActionErrorBanner() }
        }
    }

    private func loadConfigs() async {
        async let pomodoro: Void = env.settings.pomodoro.load()
        async let meetings: Void = env.settings.meetings.load()
        _ = await (pomodoro, meetings)
    }

    private func refresh() {
        Task {
            await env.live.refreshNow()
            await loadConfigs()
        }
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItemGroup {
            ForEach(PomodoroControls.items(for: env.live.pomodoro.value)) { item in
                Button {
                    Task { await env.actions.run(.pomodoro(item.action)) }
                } label: {
                    Label { Text(item.title) } icon: { Image(systemName: item.systemImage) }
                }
                .help(Text(item.title))
                .disabled(!env.live.connection.isOnline || env.live.pomodoro.error == .featureOff
                          || env.actions.running.contains(.pomodoro(item.action)))
            }
        }
        if let failure = env.actions.lastError, case .pomodoro = failure.action {
            ToolbarItem {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red)
                    .help(Text(failure.error.message))
                    .accessibilityLabel(Text(failure.error.message))
            }
        }
        ToolbarItemGroup {
            Button("Refresh", systemImage: "arrow.clockwise", action: refresh)
                .help("Refresh (⌘R)")
            Button("Open in Browser", systemImage: "safari") { openStatsDashboard() }
                .help("Open the server's statistics page")
                .disabled(env.serverURL == nil)
        }
    }

    private func openStatsDashboard() {
        guard let base = env.serverURL else { return }
        openURL(base.appending(path: "v1/pomodoro/dashboard"))
    }
}

struct LiveDashboardSource: DashboardSource, Equatable {
    let env: AppEnvironment

    nonisolated static func == (a: Self, b: Self) -> Bool { a.env === b.env }

    var connection: ConnectionHealth { env.live.connection }
    var snapshot: Loadable<Snapshot> { env.live.snapshot }
    var pomodoro: Loadable<PomoState> { env.live.pomodoro }
    var stats: Loadable<PomoStats> { env.live.stats }
    var usage: Loadable<UsageSnapshot> { env.live.usage }
    var meetings: Loadable<MeetingsState> { env.live.meetings }
    var screen: Loadable<[Int]> { env.live.screen }
    var clockHealth: Loadable<ClockHealth> { env.live.clockHealth }
    var weather: Loadable<WeatherState> { env.live.weather }
    var activity: Loadable<ActivitySummary> { env.live.activity }
    var workhours: Loadable<WorkHours> { env.live.workhours }
    var heatmap: Loadable<Heatmap> { env.live.heatmap }
    var reminders: [UpcomingItem] {
        env.reminderWatcher.upcoming.map {
            UpcomingItem(id: "r|\($0.id)", kind: .reminder, title: $0.title, date: $0.due)
        }
    }
    var remindersEnabled: Bool { env.reminderWatcher.prefs.enabled }
    var pomoConfig: PomoConfig? { env.settings.pomodoro.applied }
    var meetingsEnabled: Bool? { env.settings.meetings.applied?.enabled }
    var calendar: Calendar { .current }
    var fixedNow: Date? { nil }
    var actions: DashboardActions {
        let runner = env.actions
        return DashboardActions(clock: { action in Task { await runner.run(.clock(action)) } },
                                running: runner.running, displayPower: env.live.displayPower,
                                pendingDisplayPower: runner.pendingDisplayPower)
    }
}

extension Notification.Name {
    static let emberRefreshRequested = Notification.Name("com.ember.refreshRequested")
}

private struct ActionErrorBanner: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        if let failure = env.actions.lastError, case .clock = failure.action {
            Label { Text(failure.error.message) } icon: { Image(systemName: "exclamationmark.triangle.fill") }
                .font(.callout)
                .foregroundStyle(.red)
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
                .background(.background, in: Capsule())
                .overlay(Capsule().strokeBorder(.separator))
                .padding(.bottom, 16)
                .transition(.move(edge: .bottom).combined(with: .opacity))
        }
    }
}
