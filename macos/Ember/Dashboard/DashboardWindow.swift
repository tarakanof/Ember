import SwiftUI
import EmberKit

/// The Dashboard window's root. A stand-in on the foundations: the old
/// dashboard's content (live mirror, sessions, Pomodoro) moved onto `LiveModel`
/// and `ActionRunner`, in the resizable window shell. The card grid replaces
/// the body in #111.
struct DashboardWindow: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openURL) private var openURL
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Ember")
                .navigationSubtitle(env.live.connection.subtitle(serverHost: env.serverURL?.host()))
                .toolbar { toolbar }
        }
        .frame(minWidth: 720, minHeight: 560)
        // Feeds are held only while the window is open; the hold ends when
        // the task is cancelled on close.
        .task { await env.live.track(.stats, .clockHealth) }
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
                VStack(alignment: .leading, spacing: 16) {
                    GroupBox {
                        LiveMatrixMirror()
                            .frame(maxWidth: 640)
                            .frame(maxWidth: .infinity)
                            .padding(8)
                            .background(.black, in: RoundedRectangle(cornerRadius: 6))
                    } label: {
                        Label("Clock", systemImage: "clock")
                    }
                    GroupBox {
                        sessions.frame(maxWidth: .infinity, alignment: .leading).padding(4)
                    } label: {
                        Label("Agents", systemImage: "sparkles")
                    }
                    GroupBox {
                        pomodoro.frame(maxWidth: .infinity, alignment: .leading).padding(4)
                    } label: {
                        Label("Focus", systemImage: "timer")
                    }
                }
                .padding(20)
            }
        }
    }

    private var sessions: some View {
        FeedStateView(feed: env.live.snapshot, isEmpty: { $0.sessions.isEmpty },
                      emptyTitle: "No active sessions", emptySymbol: "sparkles") { snap in
            VStack(alignment: .leading, spacing: 8) {
                ForEach(snap.sessions.sorted { $0.stateEnum.sortRank < $1.stateEnum.sortRank },
                        id: \.session) { s in
                    let p = SessionPresentation(s)
                    HStack(spacing: 12) {
                        PhaseBadge(state: s.stateEnum)
                            .frame(width: 90, alignment: .leading)
                        VStack(alignment: .leading) {
                            Text(verbatim: "\(p.toolDisplayName) · \(s.source)")
                            if let sub = p.subtitle {
                                Text(verbatim: sub)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                            }
                        }
                        Spacer()
                        if let ctx = p.contextText() {
                            Text(verbatim: ctx).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }

    private var pomodoro: some View {
        FeedStateView(feed: env.live.pomodoro,
                      offTitle: "Pomodoro is off",
                      offDescription: "Turn it on in Settings › Pomodoro.",
                      offSettingsPane: "pomodoro") { p in
            VStack(alignment: .leading, spacing: 8) {
                if p.mode != .idle {
                    HStack(alignment: .firstTextBaseline) {
                        Text(verbatim: p.phaseEnum.displayName).bold()
                        Text(verbatim: DurationText.remaining(p.remainingSec))
                            .font(.title3.monospacedDigit())
                        Text("round \(p.round)").foregroundStyle(.secondary)
                    }
                }
                if let st = env.live.stats.value {
                    Text("Today: \(st.today.completedFocus) focus · \(DurationText.minutes(st.today.focusMin)) · streak \(st.streak)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if let failure = env.actions.lastError {
                    Label(failure.error.localizedDescription, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.red)
                }
            }
        }
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItemGroup {
            ForEach(PomodoroControls.items(for: env.live.pomodoro.value)) { item in
                Button(item.title, systemImage: item.systemImage) {
                    Task { await env.actions.run(.pomodoro(item.action)) }
                }
                .disabled(!env.live.connection.isOnline || env.live.pomodoro.error == .featureOff)
            }
        }
        ToolbarItemGroup {
            Button("Refresh", systemImage: "arrow.clockwise") {
                Task { await env.live.refreshNow() }
            }
            Button("Open in Browser", systemImage: "safari") { openStatsDashboard() }
                .disabled(env.serverURL == nil)
        }
    }

    /// The server-rendered stats page, until the native cards land (#111).
    private func openStatsDashboard() {
        guard let base = env.serverURL else { return }
        openURL(base.appending(path: "v1/pomodoro/dashboard"))
    }
}
