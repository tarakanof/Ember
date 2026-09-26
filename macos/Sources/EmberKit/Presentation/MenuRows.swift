import Foundation

/// The rows of the menu-bar menu, built from `LiveModel` values. Pure, so the
/// menu's rules (which session heads it, when a row hides, how a time reads)
/// are unit-tested; the view only lays them out. A feature the server doesn't
/// have (`.featureOff`, or never loaded) hides its rows instead of showing an
/// error.
public enum MenuRows {

    // MARK: Header

    /// The status rows at the top: a title and an optional activity line.
    public struct Header: Sendable {
        public let title: LocalizedStringResource
        /// Sanitised producer text ("Bash: npm test"); nil when there's none.
        public let detail: String?
    }

    /// The menu cuts the activity line to this many characters.
    public static let activityMaxLength = 48

    /// "Claude on m4 — Running" + activity; "Idle" when connected with no
    /// session to show; "Offline — server unreachable since 10:42" (or just
    /// "Offline" when this server never answered).
    public static func header(connection: ConnectionHealth, hasEverLoaded: Bool, winning: Session?,
                              locale: Locale = .current, timeZone: TimeZone = .current) -> Header {
        switch connection {
        case .unconfigured:
            return Header(title: "Not set up", detail: nil)
        case .connecting:
            return Header(title: "Connecting…", detail: nil)
        case .offline(let since):
            guard hasEverLoaded else { return Header(title: "Offline", detail: nil) }
            return Header(title: "Offline — server unreachable since \(time(since, locale: locale, timeZone: timeZone))",
                          detail: nil)
        case .online, .degraded:
            guard let winning else { return Header(title: "Idle", detail: nil) }
            let p = SessionPresentation(winning)
            return Header(title: p.title, detail: p.subtitle(maxLength: activityMaxLength))
        }
    }

    /// What VoiceOver reads for the menu-bar icon after "Ember": the bot's
    /// colour and eyes show the state, so it has to be said too.
    public static func accessibilityValue(connection: ConnectionHealth, winning: Session?) -> LocalizedStringResource {
        switch connection {
        case .unconfigured: "Not set up"
        case .connecting: "Connecting…"
        case .offline: "Offline"
        case .online, .degraded: winning.map { SessionPresentation($0).title } ?? "Idle"
        }
    }

    // MARK: Other sessions

    public struct SessionRow: Identifiable, Sendable {
        public let id: String
        /// "Codex on m5 — Waiting · 41% context".
        public let text: LocalizedStringResource
    }

    public struct OtherSessions: Sendable {
        public let rows: [SessionRow]
        /// "4 more", when sessions were cut at the limit.
        public let overflow: LocalizedStringResource?
    }

    /// Every session but the one in the header, attention first (waiting,
    /// error, running, done, idle), newest first within a state, cut at
    /// `limit`. Empty with a single session.
    public static func otherSessions(_ sessions: [Session], winning: Session?, limit: Int = 8,
                                     locale: Locale = .current) -> OtherSessions {
        guard sessions.count > 1 else { return OtherSessions(rows: [], overflow: nil) }
        let winnerKey = winning.map(key)
        let rest = sessions
            .filter { key($0) != winnerKey }
            .sorted {
                let (a, b) = ($0.stateEnum.sortRank, $1.stateEnum.sortRank)
                return a != b ? a < b : $0.updatedAt > $1.updatedAt
            }
        let shown = rest.prefix(max(0, limit)).map { s in
            let p = SessionPresentation(s)
            let title = String(localized: p.title)
            let text: LocalizedStringResource = p.contextText(locale: locale).map {
                "\(title) · \(String(localized: $0))"
            } ?? "\(title)"
            return SessionRow(id: key(s), text: text)
        }
        let hidden = rest.count - shown.count
        return OtherSessions(rows: shown, overflow: hidden > 0 ? "\(hidden) more" : nil)
    }

    private static func key(_ s: Session) -> String { "\(s.source)\u{1F}\(s.tool)\u{1F}\(s.session)" }

    // MARK: Usage

    public struct UsageRow: Identifiable, Sendable {
        public enum Level: Sendable, Equatable {
            case normal
            /// At or above `highPercent`.
            case high
            /// At or above 100 %: the window is used up until it resets.
            case limit
        }

        public let tool: String
        /// "Claude 5h 47% · resets 04:20".
        public let text: LocalizedStringResource
        public let level: Level
        public var id: String { tool }
    }

    /// The share of a 5-hour window from which a row counts as high.
    public static let highPercent: Double = 80

    /// One row per tool with a live 5-hour window, sorted by tool. `/v1/usage`
    /// (server 0.28+) is the source; a tool it doesn't list (or a server
    /// without it) falls back to the percent the producer put on its latest
    /// `/state` session. A stale tool is left out: the clock hides it too.
    public static func usage(_ usage: Loadable<UsageSnapshot>, sessions: [Session], now: Date,
                             locale: Locale = .current, timeZone: TimeZone = .current) -> [UsageRow] {
        var rows: [String: UsageRow] = [:]
        let listed = Set(usage.value?.tools.map(\.tool) ?? [])
        for t in usage.value?.tools ?? [] where !t.stale {
            guard let w = t.fiveHour else { continue }
            let reset = w.resetsAt.flatMap { resetText($0, now: now, locale: locale, timeZone: timeZone) }
                ?? w.resetLabel.flatMap { SessionPresentation.displayText($0, maxLength: 24) }
            rows[t.tool] = usageRow(tool: t.tool, percent: w.usedPercent, reset: reset, locale: locale)
        }
        let latest = Dictionary(grouping: sessions.filter { $0.rateWindowPct != nil && !listed.contains($0.tool) },
                                by: \.tool)
            .compactMapValues { $0.max { $0.updatedAt < $1.updatedAt } }
        for (tool, s) in latest {
            guard let pct = s.rateWindowPct else { continue }
            let reset = s.rateResetAt > 0
                ? resetText(Date(timeIntervalSince1970: TimeInterval(s.rateResetAt)), now: now, locale: locale, timeZone: timeZone)
                : nil
            rows[tool] = usageRow(tool: tool, percent: Double(pct), reset: reset, locale: locale)
        }
        return rows.values.sorted { $0.tool < $1.tool }
    }

    private static func usageRow(tool: String, percent: Double, reset: String?, locale: Locale) -> UsageRow {
        let name = String(localized: AppNames.display(tool))
        let pct = min(max(percent, 0), 100)
        let level: UsageRow.Level = percent >= 100 ? .limit : percent >= highPercent ? .high : .normal
        let text: LocalizedStringResource
        switch (level, reset) {
        case (.limit, let reset?): text = "\(name) 5h limit reached · resets \(reset)"
        case (.limit, nil): text = "\(name) 5h limit reached"
        case (_, let reset?): text = "\(name) 5h \(Percent.text(pct, locale: locale)) · resets \(reset)"
        case (_, nil): text = "\(name) 5h \(Percent.text(pct, locale: locale))"
        }
        return UsageRow(tool: tool, text: text, level: level)
    }

    /// "04:20" within a day, "Sun 16:00" beyond; nil once it has passed.
    private static func resetText(_ date: Date, now: Date, locale: Locale, timeZone: TimeZone) -> String? {
        let ahead = date.timeIntervalSince(now)
        guard ahead > 0 else { return nil }
        if ahead < 24 * 3600 { return time(date, locale: locale, timeZone: timeZone) }
        var style = Date.FormatStyle(date: .omitted, time: .shortened).weekday(.abbreviated).locale(locale)
        style.timeZone = timeZone
        return date.formatted(style)
    }

    // MARK: Next event

    /// A due Apple Reminder (the app's `ReminderWatcher.upcoming`).
    public struct Reminder: Equatable, Sendable {
        public let title: String
        public let due: Date
        public init(title: String, due: Date) {
            self.title = title
            self.due = due
        }
    }

    /// How far ahead the next-event row looks.
    public static let nextEventHorizon: TimeInterval = 36 * 3600
    /// A meeting that started this recently still reads "now".
    static let startedGrace: TimeInterval = 5 * 60

    /// "Next: Standup in 12 min", "Next: Reminder: Pay rent at 14:30",
    /// "Next: Retro tomorrow at 09:00". nil when nothing starts within 36 h.
    public static func nextEvent(meetings: MeetingsState?, reminders: [Reminder], now: Date,
                                 locale: Locale = .current, timeZone: TimeZone = .current) -> LocalizedStringResource? {
        let candidates: [(title: String, start: Date)] =
            (meetings?.upcoming ?? []).compactMap { m in
                SessionPresentation.displayText(m.title, maxLength: 40).map { ($0, m.start) }
            } + reminders.compactMap { r in
                SessionPresentation.displayText(r.title, maxLength: 40).map {
                    (String(localized: "Reminder: \($0)"), r.due)
                }
            }
        guard let next = candidates
            .filter({ $0.start.timeIntervalSince(now) > -startedGrace && $0.start.timeIntervalSince(now) <= nextEventHorizon })
            .min(by: { $0.start < $1.start })
        else { return nil }
        let title = next.title
        let ahead = next.start.timeIntervalSince(now)
        if ahead < 60 { return "Next: \(title) now" }
        if ahead < 3600 { return "Next: \(title) in \(Int((ahead / 60).rounded(.up))) min" }
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = timeZone
        let at = time(next.start, locale: locale, timeZone: timeZone)
        if cal.isDate(next.start, inSameDayAs: now) { return "Next: \(title) at \(at)" }
        if let tomorrow = cal.date(byAdding: .day, value: 1, to: now), cal.isDate(next.start, inSameDayAs: tomorrow) {
            return "Next: \(title) tomorrow at \(at)"
        }
        var style = Date.FormatStyle(date: .omitted, time: .shortened).weekday(.abbreviated).locale(locale)
        style.timeZone = timeZone
        return "Next: \(title) \(next.start.formatted(style))"
    }

    // MARK: Pomodoro

    /// "Focus · 18:42 left · round 2", "Short Break · paused · 18:42 left",
    /// "Long Break · ready to start" (parked); nil while idle or unknown.
    public static func pomodoroStatus(_ state: PomoState?, locale: Locale = .current) -> LocalizedStringResource? {
        guard let state else { return nil }
        let phase = String(localized: state.phaseEnum.displayName)
        let left = DurationText.remaining(state.remainingSec, locale: locale)
        switch state.mode {
        case .idle: return nil
        case .running: return "\(phase) · \(left) left · round \(state.round)"
        case .paused: return "\(phase) · paused · \(left) left"
        case .parked: return "\(phase) · ready to start"
        }
    }

    public struct PomodoroGroup: Equatable, Sendable {
        public let items: [PomodoroItem]
        /// False while offline: the controls show but can't be used.
        public let isEnabled: Bool
    }

    /// The Pomodoro controls that apply now; nil when the server has no
    /// Pomodoro (the group hides).
    public static func pomodoroControls(_ pomodoro: Loadable<PomoState>, connection: ConnectionHealth) -> PomodoroGroup? {
        if pomodoro.error == .featureOff { return nil }
        return PomodoroGroup(items: PomodoroControls.items(for: pomodoro.value), isEnabled: connection.isOnline)
    }

    /// "Today 3 of 8 · 1h 15m"; "Today 3 sessions · 1h 15m" with the daily
    /// goal off (0, or a server too old to report it). nil until stats load.
    public static func today(_ stats: PomoStats?, locale: Locale = .current) -> LocalizedStringResource? {
        guard let stats else { return nil }
        let done = stats.today.completedFocus
        let time = DurationText.minutes(stats.today.focusMin, locale: locale)
        let goal = stats.goal.dailySessions
        if goal > 0 { return "Today \(done) of \(goal) · \(time)" }
        return done == 1 ? "Today 1 session · \(time)" : "Today \(done) sessions · \(time)"
    }

    // MARK: Action errors

    /// "Couldn't start: unauthorized", for `ActionRunner.lastError`.
    public static func failure(_ action: EmberAction, _ error: FeedError) -> LocalizedStringResource {
        let reason = String(localized: shortReason(error))
        switch action {
        case .pomodoro(.start): return "Couldn't start: \(reason)"
        case .pomodoro(.pause): return "Couldn't pause: \(reason)"
        case .pomodoro(.resume): return "Couldn't resume: \(reason)"
        case .pomodoro(.skip): return "Couldn't skip: \(reason)"
        case .pomodoro(.stop): return "Couldn't stop: \(reason)"
        case .setApp(let name, let enabled):
            let app = String(localized: AppNames.display(name))
            return enabled ? "Couldn't show \(app): \(reason)" : "Couldn't hide \(app): \(reason)"
        case .clock(.next), .clock(.previous): return "Couldn't switch apps: \(reason)"
        case .clock(.dismiss): return "Couldn't dismiss the notification: \(reason)"
        case .clock(.power(true)): return "Couldn't turn the display on: \(reason)"
        case .clock(.power(false)): return "Couldn't turn the display off: \(reason)"
        case .clock(.reboot): return "Couldn't restart the clock: \(reason)"
        }
    }

    /// A menu row is one line: the reason is a few words.
    private static func shortReason(_ error: FeedError) -> LocalizedStringResource {
        switch error {
        case .offline: "server unreachable"
        case .unauthorized: "unauthorized"
        case .rateLimited: "rate-limited"
        case .featureOff: "not supported by this server"
        case .server: "server error"
        }
    }

    // MARK: Clock

    public struct PowerItem: Identifiable, Sendable {
        /// The state the item switches the matrix to.
        public let on: Bool
        public let title: LocalizedStringResource
        public var id: Bool { on }
    }

    /// Display power items. `PUT /v1/device/display/power` shipped with the
    /// 0.28 read endpoints, so a server that answers `/v1/usage` or
    /// `/v1/clock/health` has it; an older one gets no item. With the matrix
    /// state known (`matrixPower`, from a clock-health feed someone holds)
    /// only the useful item shows, otherwise both.
    public static func displayPower(usage: Loadable<UsageSnapshot>, clockHealth: Loadable<ClockHealth>,
                                    matrixPower: Bool?) -> [PowerItem] {
        guard usage.value != nil || clockHealth.value != nil else { return [] }
        let off = PowerItem(on: false, title: "Turn Display Off")
        let on = PowerItem(on: true, title: "Turn Display On")
        switch matrixPower {
        case true?: return [off]
        case false?: return [on]
        case nil: return [off, on]
        }
    }

    public struct AppRow: Identifiable, Sendable {
        /// Wire name, for `EmberAction.setApp`.
        public let name: String
        public let title: LocalizedStringResource
        public let enabled: Bool
        public var id: String { name }
    }

    /// The Show on Clock toggles, in the server's order; empty (the submenu
    /// hides) when the server has no `/v1/apps` or no apps yet.
    public static func showOnClock(_ apps: Loadable<[AppToggle]>) -> [AppRow] {
        (apps.value ?? []).map { AppRow(name: $0.name, title: AppNames.display($0.name), enabled: $0.enabled) }
    }

    // MARK: Helpers

    private static func time(_ date: Date, locale: Locale, timeZone: TimeZone) -> String {
        var style = Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)
        style.timeZone = timeZone
        return date.formatted(style)
    }
}
