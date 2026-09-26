import SwiftUI
import EmberKit

/// The Dashboard window (design §2): "what is my setup doing now, and how
/// did my day and week go". A card grid over `LiveModel`; the feeds it
/// needs are held only while the window is open.
struct DashboardWindow: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openURL) private var openURL
    @Environment(\.openWindow) private var openWindow

    @State private var clockWebURL: URL?

    /// Everything the cards read beyond tiers A and B.
    static let heldFeeds: [Feed] = [.stats, .usage, .meetings, .clockHealth, .screen, .activity,
                                    .workhours, .heatmap, .weather]

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Ember")
                .navigationSubtitle(Text(env.live.connection.subtitle(serverHost: env.serverURL?.host())))
                .toolbar { toolbar }
        }
        .frame(minWidth: 720, minHeight: 560)
        // The hold ends when the task is cancelled on close.
        .task { await env.live.track(Self.heldFeeds) }
        .task(id: env.serverURL) { await loadConfigs() }
    }

    @ViewBuilder
    private var content: some View {
        if env.live.connection == .unconfigured {
            ContentUnavailableView {
                Label("Ember isn't connected to a server", systemImage: "network.slash")
            } description: {
                Text("Enter the server's address in Connection settings, or pick one Ember found on your network.")
            } actions: {
                Button("Open Connection Settings") { openSettings(pane: "connection", using: openWindow) }
            }
        } else {
            ScrollView {
                DashboardContent(data: data, actions: actions)
            }
            .overlay(alignment: .bottom) { actionError }
        }
    }

    private var data: DashboardData {
        let live = env.live
        let reminders = env.reminderWatcher
        let settings = env.settings
        return DashboardData(
            snapshot: live.snapshot, pomodoro: live.pomodoro, stats: live.stats, usage: live.usage,
            meetings: live.meetings, screen: live.screen, clockHealth: live.clockHealth,
            weather: live.weather, activity: live.activity, workhours: live.workhours, heatmap: live.heatmap,
            reminders: reminders.upcoming.map {
                UpcomingItem(id: "r|\($0.id)", kind: .reminder, title: $0.title, date: $0.due)
            },
            remindersEnabled: reminders.prefs.enabled,
            pomoConfig: settings.pomodoro.applied,
            meetingsEnabled: settings.meetings.applied?.enabled,
            clockWebURL: clockWebURL,
            now: Date())
    }

    private var actions: DashboardActions {
        let runner = env.actions
        return DashboardActions(
            clock: { action in Task { await runner.run(.clock(action)) } },
            running: runner.running,
            openURL: { openURL($0) })
    }

    /// The configs the cards read (focus length for the goal line, whether
    /// meetings are on) and the clock's web address. Read-only; a config
    /// with unsaved edits in Settings isn't reloaded over.
    private func loadConfigs() async {
        async let pomodoro: Void = env.settings.pomodoro.load()
        async let meetings: Void = env.settings.meetings.load()
        _ = await (pomodoro, meetings)
        clockWebURL = try? await env.device.config().webURL
    }

    /// The last failed clock action, until it clears itself (10 s).
    @ViewBuilder
    private var actionError: some View {
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
            Button("Refresh", systemImage: "arrow.clockwise") {
                Task { await env.live.refreshNow() }
            }
            .help("Refresh (⌘R)")
            Button("Open in Browser", systemImage: "safari") { openStatsDashboard() }
                .help("Open the server's statistics page")
                .disabled(env.serverURL == nil)
        }
    }

    /// The server-rendered stats page, for what the cards don't cover.
    private func openStatsDashboard() {
        guard let base = env.serverURL else { return }
        openURL(base.appending(path: "v1/pomodoro/dashboard"))
    }
}
