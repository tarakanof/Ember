import SwiftUI
import EmberKit

/// A knob page's round face, like `PanelPreview` for the clock: drawn here
/// from the live data the knob shows, dimmed with "Off" when the page is off.
struct KnobPreview<Face: View>: View {
    let title: LocalizedStringKey
    let caption: Text
    let enabled: Bool
    var maxSide: CGFloat = KnobPreviewSizes.page
    @ViewBuilder let face: Face

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                Text(title).font(.callout.weight(.semibold))
                if !enabled {
                    Text("Off").font(.callout).foregroundStyle(.secondary)
                }
            }
            KnobScreen(on: enabled) { face }
                .frame(maxWidth: maxSide, maxHeight: maxSide)
                .frame(maxWidth: .infinity)
                .accessibilityHidden(true)
            caption.font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(title))
        .accessibilityValue(enabled ? caption : Text("Off. \(caption)"))
        .accessibilityAddTraits(.isImage)
    }
}

enum KnobPreviewSizes {
    static let page: CGFloat = 240
    static let overview: CGFloat = 120
}

/// The live inputs every knob face reads.
@MainActor
struct KnobPreviewData {
    let env: AppEnvironment
    let brightnessLevel: Int?
    /// The mood to keep while Ember can't be read, as the knob does.
    let lastMood: KnobMood
    /// Reads what the server says is playing; nil until the preview is on screen.
    let nowPlaying: KnobNowPlayingFeed?

    var settings: KnobSettings { env.knob.settings.draft }
    /// The mood from the latest good `/state`; with none, the last one shown.
    var mood: KnobMood { liveMood ?? lastMood }

    var liveMood: KnobMood? {
        guard case .loaded(let snap, _) = env.live.snapshot else { return nil }
        return KnobMood(sessions: snap.sessions, maxChars: KnobTheme.standard.bot.host.maxChars)
    }
    var pomodoro: PomoState? { env.live.pomodoro.value }
    var pomodoroFetchedAt: Date? { env.live.lastFetched(.pomodoroState) }
    var weather: WeatherState? { env.live.weather.value }

    var pomodoroNote: String? {
        let p = env.settings.pomodoro
        return p.isLoaded && !p.draft.enabled ? "POMODORO OFF" : nil
    }

    /// The knob's backlight, 0…1: Ember's level (or its own), never below the floor.
    var brightness: Double {
        let b = settings.brightness
        let level = b.followEmber ? (brightnessLevel ?? b.level) : b.level
        return Double(max(level, b.floor)) / 255
    }

    func isOn(_ page: String) -> Bool { settings.pages.first { $0.id == page }?.on ?? false }

    @ViewBuilder
    func face(_ page: String, animated: Bool) -> some View {
        switch page {
        case "bot":
            KnobBotLive(mood: mood, sleepAfter: Double(settings.bot.sleepyAfterS),
                        sourceLabel: settings.bot.sourceLabel ?? true, workingRing: settings.bot.workingRing ?? true,
                        animated: animated, brightness: brightness)
        case "pomodoro":
            KnobPomoLive(state: pomodoro, fetchedAt: pomodoroFetchedAt, note: pomodoroNote, animated: animated,
                         brightness: brightness)
        case "weather":
            KnobWeatherLive(state: weather, animated: animated, brightness: brightness)
        case "nowplaying":
            KnobNowPlayingLive(state: nowPlaying?.state, offline: nowPlaying?.failed ?? false, offset: nowPlaying?.serverOffset ?? 0,
                               pictures: nowPlaying?.pictures ?? .init(),
                               animated: animated, brightness: brightness)
        default:
            Circle().fill(.black)
        }
    }

    func caption(_ page: String) -> Text {
        switch page {
        case "bot": botCaption
        case "pomodoro": pomodoroCaption
        case "weather": weatherCaption
        case "nowplaying": nowPlayingCaption
        default: Text(verbatim: "")
        }
    }

    private var botCaption: Text {
        let m = mood
        let host = m.host
        switch m.mood {
        case .waiting:
            return host.isEmpty ? Text("Waiting: an agent needs you.")
                : Text("Waiting: an agent on \(host) needs you.",
                       comment: "Knob bot preview: the host whose agent is waiting (\"DT-MBP\").")
        case .error:
            return host.isEmpty ? Text("Error: an agent hit a problem.")
                : Text("Error: an agent on \(host) hit a problem.",
                       comment: "Knob bot preview: the host whose agent failed (\"DT-MBP\").")
        case .working: return Text("Working: an agent is running.")
        case .done: return Text("Done: an agent finished.")
        case .idle, .sleepy: return Text("Idle: no agent is working.")
        }
    }

    private var pomodoroCaption: Text {
        if pomodoroNote != nil { return Text("The Pomodoro is off in Ember.") }
        guard let s = pomodoro else { return Text("No Pomodoro data from Ember.") }
        let f = KnobPomoFace(state: s, fetchedAt: pomodoroFetchedAt, now: .now, theme: KnobTheme.standard.pomodoro)
        let phase = s.phase == "focus" ? Text("Focus") : s.phase == "long_break" ? Text("Long break") : Text("Break")
        switch f.mode {
        case .idle: return Text("Idle. Push the knob to start a focus round.")
        case .running:
            return Text("\(phase), \(f.time) left.",
                        comment: "Knob Pomodoro preview: phase and time left (\"Focus, 12:20 left.\").")
        case .paused:
            return Text("\(phase) paused, \(f.time) left.",
                        comment: "Knob Pomodoro preview: phase and time left while paused (\"Focus paused, 12:20 left.\").")
        case .parked:
            return Text("\(phase) is next. Push the knob to start it.",
                        comment: "Knob Pomodoro preview: the phase waiting to start (\"Break is next…\").")
        }
    }

    private var nowPlayingCaption: Text {
        guard let s = nowPlaying?.state else {
            return nowPlaying?.failed == true ? Text("No music data from Ember.") : Text("Reading what's playing…")
        }
        guard s.isActive else { return Text("Nothing playing.") }
        let what = Text(verbatim: [s.title, s.artist].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " - "))
        return s.state == "paused"
            ? Text("Paused: \(what)", comment: "Knob now-playing preview: the paused track (\"Paused: Song - Artist\").")
            : Text("Playing: \(what)", comment: "Knob now-playing preview: the playing track (\"Playing: Song - Artist\").")
    }

    private var weatherCaption: Text {
        guard let w = weather, let cur = w.current else { return Text("No weather from Ember yet.") }
        let look = KnobWeatherLook(state: w, now: .now, maxAge: KnobTheme.standard.weather.maxAgeS)
        let temp = Text(verbatim: "\(Int(cur.tempC.rounded()))°")
        let sky: Text = switch look.face {
        case .clearDay: Text("Clear")
        case .clearNight: Text("Clear night")
        case .partlyCloudy: Text("Partly cloudy")
        case .overcast: Text("Cloudy")
        case .fog: Text("Fog")
        case .rain: Text("Rain")
        case .snow: Text("Snow")
        case .storm: Text("Thunderstorm")
        }
        if look.still {
            return Text("\(sky), \(temp). Out of date, so the knob shows it in grey.",
                        comment: "Knob weather preview: condition and temperature (\"Rain, 14°\") of a stale observation.")
        }
        return Text("\(sky), \(temp).", comment: "Knob weather preview: condition and temperature (\"Rain, 14°.\").")
    }
}

/// The round face for one knob app's page, above its settings.
struct KnobAppPreviewSection: View {
    @Environment(AppEnvironment.self) private var env
    let page: String
    @State private var brightness: Int?
    @State private var lastMood = KnobMood(mood: .idle)
    @State private var nowPlaying: KnobNowPlayingFeed?

    var body: some View {
        let data = KnobPreviewData(env: env, brightnessLevel: brightness, lastMood: lastMood,
                                   nowPlaying: nowPlaying)
        Section {
            KnobPreview(title: knobPageTitle(page), caption: data.caption(page), enabled: data.isOn(page)) {
                data.face(page, animated: true)
            }
            .settingsPreviewRow()
        } footer: {
            Text("Drawn by Ember from the same data the knob shows.")
        }
        .knobPreviewFeeds(page: page, brightness: $brightness, nowPlaying: $nowPlaying)
        .onChange(of: data.liveMood, initial: true) { _, m in if let m { lastMood = m } }
    }
}

/// Every knob page side by side, in the knob's order.
struct KnobPagesPreviewSection: View {
    @Environment(AppEnvironment.self) private var env
    @State private var brightness: Int?
    @State private var lastMood = KnobMood(mood: .idle)
    @State private var nowPlaying: KnobNowPlayingFeed?

    var body: some View {
        let data = KnobPreviewData(env: env, brightnessLevel: brightness, lastMood: lastMood, nowPlaying: nowPlaying)
        let s = data.settings
        Section {
            HStack(alignment: .top, spacing: 12) {
                ForEach(s.pages) { page in
                    KnobPreview(title: knobPageTitle(page.id),
                                caption: page.id == s.home ? Text("Home page") : Text(verbatim: ""),
                                enabled: page.on, maxSide: KnobPreviewSizes.overview) {
                        data.face(page.id, animated: false)
                    }
                    .frame(maxWidth: .infinity)
                    .draggable(KnobPageDrag(id: page.id))
                    .dropDestination(for: KnobPageDrag.self) { drops, _ in
                        guard let drop = drops.first else { return false }
                        env.knob.edit { $0.movePage(drop.id, to: page.id) }
                        return true
                    }
                }
            }
            .settingsPreviewRow()
        }
        .knobPreviewFeeds(page: nil, brightness: $brightness, nowPlaying: $nowPlaying)
        .onChange(of: data.liveMood, initial: true) { _, m in if let m { lastMood = m } }
    }
}

extension View {
    /// Keeps the feeds a knob preview reads fresh while it is on screen.
    func knobPreviewFeeds(page: String?, brightness: Binding<Int?>,
                          nowPlaying: Binding<KnobNowPlayingFeed?>) -> some View {
        modifier(KnobPreviewFeeds(page: page, brightness: brightness, nowPlaying: nowPlaying))
    }
}

private struct KnobPreviewFeeds: ViewModifier {
    @Environment(AppEnvironment.self) private var env
    let page: String?
    @Binding var brightness: Int?
    @Binding var nowPlaying: KnobNowPlayingFeed?

    private struct Level: Decodable { let level: Int }

    private var overviewNeedsNowPlaying: Bool {
        page == nil && env.knob.settings.draft.pages.contains { $0.id == "nowplaying" && $0.on }
    }

    func body(content: Content) -> some View {
        content
            .task {
                guard page == nil || page == "weather" else { return }
                await env.live.track(.weather)
            }
            .task(id: "\(env.connection.serverURL?.absoluteString ?? "")|\(overviewNeedsNowPlaying)") {
                // The overview draws a thumbnail and only when the page is on; the page's own
                // preview shows even when it is off.
                let overview = page == nil
                guard page == "nowplaying" || (overview && overviewNeedsNowPlaying) else { return }
                let feed = KnobNowPlayingFeed(client: env.connection.client, thumbnail: overview)
                nowPlaying = feed
                await feed.run()
            }
            .task(id: env.knob.settings.draft.brightness.followEmber) {
                guard env.knob.settings.draft.brightness.followEmber else { return }
                while !Task.isCancelled {
                    if let l: Level = try? await env.connection.client.get("/v1/display/brightness") {
                        brightness = l.level
                    }
                    try? await Task.sleep(for: .seconds(60))
                }
            }
    }
}
