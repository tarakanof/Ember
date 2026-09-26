import AppKit
import SwiftUI
import EmberKit

/// The Dashboard window (design §2): "what is my setup doing now, and how
/// did my day and week go". A card grid over `LiveModel`; the feeds it
/// needs are held only while the window is open and actually visible.
struct DashboardWindow: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openURL) private var openURL
    @Environment(\.openWindow) private var openWindow

    @State private var clockWebURL: URL?
    /// False while the window is minimised or fully covered.
    @State private var isVisible = true

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
        .background(WindowVisibilityReader(isVisible: $isVisible))
        // Held only while on screen: a minimised or covered window stops the
        // 1 s mirror reads, which cross the lossy server→clock link. The hold
        // ends when the task is cancelled (hidden, or the window closed).
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
                Button("Open Connection Settings") { openSettings(pane: "connection", using: openWindow) }
            }
        } else {
            ScrollView {
                DashboardContent(source: LiveDashboardSource(env: env, clockWebURL: clockWebURL),
                                 onRetry: refresh)
            }
            .overlay(alignment: .bottom) { ActionErrorBanner() }
        }
    }

    /// The configs the cards read (focus length for the goal line, whether
    /// meetings are on) and the clock's web address. Read-only; a config
    /// with unsaved edits in Settings isn't reloaded over.
    private func loadConfigs() async {
        async let pomodoro: Void = env.settings.pomodoro.load()
        async let meetings: Void = env.settings.meetings.load()
        _ = await (pomodoro, meetings)
        clockWebURL = try? await env.connection.device.config().webURL
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

    /// The server-rendered stats page, for what the cards don't cover.
    private func openStatsDashboard() {
        guard let base = env.serverURL else { return }
        openURL(base.appending(path: "v1/pomodoro/dashboard"))
    }
}

/// The window's source: every property forwards to `LiveModel` (and the
/// few app services), so SwiftUI tracks exactly what each card reads.
struct LiveDashboardSource: DashboardSource, Equatable {
    let env: AppEnvironment
    var clockWebURL: URL?

    /// Same model, same address: SwiftUI can skip a card whose inputs are
    /// equal and rely on observation for the rest.
    nonisolated static func == (a: Self, b: Self) -> Bool { a.env === b.env && a.clockWebURL == b.clockWebURL }

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
    /// ⌘R (Refresh in the app menu): views that load more than the live
    /// feeds reload it too.
    static let emberRefreshRequested = Notification.Name("com.ember.refreshRequested")
}

/// The last failed clock action, until it clears itself (10 s). Its own view
/// so the action state doesn't re-render the grid.
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

/// Tracks whether the hosting window is visible on screen: not minimised and
/// not fully covered (`NSWindow.occlusionState`). SwiftUI's `scenePhase`
/// stays `.active` for both on macOS.
private struct WindowVisibilityReader: NSViewRepresentable {
    @Binding var isVisible: Bool

    func makeNSView(context: Context) -> NSView {
        let view = ProbeView()
        view.onChange = { visible in
            if isVisible != visible { isVisible = visible }
        }
        return view
    }

    func updateNSView(_ nsView: NSView, context: Context) {}

    final class ProbeView: NSView {
        var onChange: ((Bool) -> Void)?

        override func viewWillMove(toWindow newWindow: NSWindow?) {
            super.viewWillMove(toWindow: newWindow)
            NotificationCenter.default.removeObserver(self, name: NSWindow.didChangeOcclusionStateNotification, object: nil)
        }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard let window else { return }
            // Selector-based: removed automatically when the view goes away.
            NotificationCenter.default.addObserver(self, selector: #selector(occlusionChanged(_:)),
                                                   name: NSWindow.didChangeOcclusionStateNotification, object: window)
            // Before the window is first ordered in it isn't visible yet; count it
            // as visible so the first paint holds the feeds.
            onChange?(!window.isVisible || window.occlusionState.contains(.visible))
        }

        @objc private func occlusionChanged(_ note: Notification) {
            guard let window else { return }
            onChange?(window.occlusionState.contains(.visible))
        }
    }
}
