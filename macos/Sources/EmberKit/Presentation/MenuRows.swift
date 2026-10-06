import Foundation

public enum MenuRows {

    public struct Header: Sendable {
        public let title: LocalizedStringResource
        public let detail: String?
    }

    public static let activityMaxLength = 48

    public static func header(connection: ConnectionHealth, hasEverLoaded: Bool, winning: Session?,
                              offlineReason: FeedError? = nil,
                              locale: Locale = .current, timeZone: TimeZone = .current) -> Header {
        switch connection {
        case .unconfigured:
            return Header(title: "Not set up", detail: nil)
        case .connecting:
            return Header(title: "Connecting…", detail: nil)
        case .offline where offlineReason == .localNetworkDenied:
            return Header(title: "Offline — Local Network access is off for Ember", detail: nil)
        case .offline(let since) where offlineReason == .timedOut && hasEverLoaded:
            return Header(title: LocalizedStringResource(
                "Offline — server not responding since \(time(since, locale: locale, timeZone: timeZone))",
                comment: "Menu header: the server stopped answering in time; the time it began (matches \"Offline — server unreachable since %@\")."),
                          detail: nil)
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

    public static func accessibilityValue(connection: ConnectionHealth, winning: Session?) -> LocalizedStringResource {
        switch connection {
        case .unconfigured: "Not set up"
        case .connecting: "Connecting…"
        case .offline: "Offline"
        case .online, .degraded: winning.map { SessionPresentation($0).title } ?? "Idle"
        }
    }

    public struct LabelState: Equatable, Sendable {
        public let state: String
        public let glyph: String
        public let trayStyle: String
        public let trayTint: String
        public let accessibilityValue: String
    }

    public static func label(connection: ConnectionHealth, winning: Session?, prefs: MenuPrefs) -> LabelState {
        LabelState(state: winning?.state ?? "idle",
                   glyph: glyphForTool(winning?.tool ?? "", prefs),
                   trayStyle: prefs.trayStyle,
                   trayTint: prefs.trayTint,
                   accessibilityValue: String(localized: accessibilityValue(connection: connection, winning: winning)))
    }

    public struct SessionRow: Identifiable, Sendable {
        public let id: String
        public let text: LocalizedStringResource
    }

    public struct OtherSessions: Sendable {
        public let rows: [SessionRow]
        public let overflow: LocalizedStringResource?
    }

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

    public struct UsageRow: Identifiable, Sendable {
        public enum Level: Sendable, Equatable {
            case normal
            case high
            case limit
        }

        public let tool: String
        public let text: LocalizedStringResource
        public let level: Level
        public var id: String { tool }
    }

    public static let highPercent: Double = 80

    public static func usage(_ usage: Loadable<UsageSnapshot>, sessions: [Session], now: Date,
                             locale: Locale = .current, timeZone: TimeZone = .current) -> [UsageRow] {
        var rows: [String: UsageRow] = [:]
        var covered = Set<String>()
        for t in usage.value?.tools ?? [] where !t.stale {
            guard let w = t.fiveHour, w.resetsAt.map({ $0 > now }) ?? true else { continue }
            covered.insert(t.tool)
            let reset: String?
            if let at = w.resetsAt {
                reset = resetText(at, now: now, locale: locale, timeZone: timeZone)
            } else {
                reset = w.resetLabel.flatMap { SessionPresentation.displayText($0, maxLength: 24) }
            }
            rows[t.tool] = usageRow(tool: t.tool, percent: w.usedPercent, reset: reset, locale: locale)
        }
        for w in sessionFiveHour(sessions, excluding: covered, now: now) {
            rows[w.tool] = usageRow(tool: w.tool, percent: Double(w.percent),
                                    reset: resetText(w.resetsAt, now: now, locale: locale, timeZone: timeZone), locale: locale)
        }
        return rows.values.sorted { $0.tool < $1.tool }
    }

    struct SessionWindow: Equatable {
        let tool: String
        let percent: Int
        let resetsAt: Date
        let session: Session
    }

    static func sessionFiveHour(_ sessions: [Session], excluding: Set<String> = [], now: Date) -> [SessionWindow] {
        let candidates = sessions.filter {
            $0.rateWindowPct != nil && $0.rateResetAt > 0 && !$0.tool.isEmpty && !excluding.contains($0.tool)
        }
        let latest = Dictionary(grouping: candidates, by: \.tool)
            .compactMapValues { $0.max { $0.updatedAt < $1.updatedAt } }
        return latest.compactMap { tool, s -> SessionWindow? in
            guard let pct = s.rateWindowPct else { return nil }
            let at = Date(timeIntervalSince1970: TimeInterval(s.rateResetAt))
            guard at > now else { return nil }
            return SessionWindow(tool: tool, percent: pct, resetsAt: at, session: s)
        }
        .sorted { $0.tool < $1.tool }
    }

    public static func liveSessions(_ snapshot: Loadable<Snapshot>) -> [Session] {
        guard case .loaded(let snap, _) = snapshot else { return [] }
        return snap.sessions
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

    private static func resetText(_ date: Date, now: Date, locale: Locale, timeZone: TimeZone) -> String? {
        let ahead = date.timeIntervalSince(now)
        guard ahead > 0 else { return nil }
        if ahead < 24 * 3600 { return time(date, locale: locale, timeZone: timeZone) }
        var style = Date.FormatStyle(date: .omitted, time: .shortened).weekday(.abbreviated).locale(locale)
        style.timeZone = timeZone
        return date.formatted(style)
    }

    public struct Reminder: Equatable, Sendable {
        public let title: String
        public let due: Date
        public init(title: String, due: Date) {
            self.title = title
            self.due = due
        }
    }

    public static let nextEventHorizon: TimeInterval = 36 * 3600
    static let startedGrace: TimeInterval = 5 * 60

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
        public let isEnabled: Bool
    }

    public static func pomodoroControls(_ pomodoro: Loadable<PomoState>, connection: ConnectionHealth) -> PomodoroGroup? {
        if pomodoro.error == .featureOff { return nil }
        return PomodoroGroup(items: PomodoroControls.items(for: pomodoro.value), isEnabled: connection.isOnline)
    }

    public static func today(_ stats: PomoStats?, locale: Locale = .current) -> LocalizedStringResource? {
        guard let stats else { return nil }
        let done = stats.today.completedFocus
        let time = DurationText.minutes(stats.today.focusMin, locale: locale)
        let goal = stats.goal.dailySessions
        if goal > 0 { return "Today \(done) of \(goal) · \(time)" }
        return "Today \(done) sessions · \(time)"
    }

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

    private static func shortReason(_ error: FeedError) -> LocalizedStringResource {
        switch error {
        case .offline: "server unreachable"
        case .timedOut: LocalizedStringResource("server not responding",
                                                comment: "Short reason after a failed menu action: the server didn't answer in time.")
        case .localNetworkDenied: "Local Network access is off"
        case .unauthorized: "unauthorized"
        case .rateLimited: "rate-limited"
        case .featureOff: "not supported by this server"
        case .clockTimedOut: LocalizedStringResource("clock didn't finish in time",
                                                     comment: "Short reason after a failed menu action: the clock was too slow to finish it.")
        case .server: "server error"
        }
    }

    public struct PowerItem: Identifiable, Sendable {
        public let on: Bool
        public let title: LocalizedStringResource
        public var id: Bool { on }
    }

    public static func displayPower(usage: Loadable<UsageSnapshot>, clockHealth: Loadable<ClockHealth>,
                                    matrixPower: Bool?) -> [PowerItem] {
        guard usage.value != nil || clockHealth.value != nil else { return [] }
        guard clockHealth.value?.isDisabled != true else { return [] }
        let off = PowerItem(on: false, title: "Turn Display Off")
        let on = PowerItem(on: true, title: "Turn Display On")
        switch matrixPower {
        case true?: return [off]
        case false?: return [on]
        case nil: return [off, on]
        }
    }

    public struct AppRow: Identifiable, Sendable {
        public let name: String
        public let title: LocalizedStringResource
        public let enabled: Bool
        public var id: String { name }
    }

    public static func showOnClock(_ apps: Loadable<[AppToggle]>) -> [AppRow] {
        (apps.value ?? []).map { AppRow(name: $0.name, title: AppNames.display($0.name), enabled: $0.enabled) }
    }

    private static func time(_ date: Date, locale: Locale, timeZone: TimeZone) -> String {
        var style = Date.FormatStyle(date: .omitted, time: .shortened).locale(locale)
        style.timeZone = timeZone
        return date.formatted(style)
    }
}
