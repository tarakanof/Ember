import Foundation
import Observation
import OSLog

/// Rings the clock when an Apple Reminder comes due. Polls a `ReminderSource`
/// at most every 30 s, sleeping only until the next fire time when that is
/// sooner, and fires each occurrence inside its window (fire time = due − lead,
/// plus 90 s of grace for a missed poll) once, via `POST /v1/reminders/fire`
/// with the occurrence's `Idempotency-Key`. A failure that proves nothing was
/// sent is retried on the next poll; any other failure is not (see
/// `ReminderFireOutcome`). Quiet hours are the server's job: sound and
/// repeat are sent exactly as `prefs` say.
///
/// Runs only while `prefs.enabled` and the source has access; call `sync()`
/// after access changes (a `prefs` change syncs by itself). The owner keeps
/// persistence and anything platform-side, such as an App Nap assertion held
/// from `onRunningChange`.
@MainActor
@Observable
public final class ReminderScheduler {
    /// A single fire request, as sent to the server.
    struct Fire: Equatable, Sendable {
        var text: String
        var sound: Bool
        var duration: Int
        var nativeIconId: String
        var hold: Bool
        var repeatSound: Bool
        var key: String
    }
    typealias Send = @Sendable (Fire) async throws -> Void

    public var prefs: ReminderPrefs {
        didSet { sync() }
    }
    /// The next few due-timed reminders not yet past due, soonest first.
    public private(set) var upcoming: [DueReminder] = []
    /// Why the last fire request failed, or nil once one succeeds.
    public private(set) var lastFireError: String?
    /// True while the poll loop runs.
    public private(set) var isRunning = false

    @ObservationIgnored private let source: any ReminderSource
    @ObservationIgnored private var send: Send
    @ObservationIgnored private let now: @MainActor () -> Date
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let onRunningChange: @MainActor (Bool) -> Void
    @ObservationIgnored private var loop: Task<Void, Never>?
    @ObservationIgnored private var tracker = ReminderFireTracker()

    /// Longest sleep between polls, so newly created reminders are seen.
    static let pollInterval: TimeInterval = 30
    /// How long after its fire time a reminder may still fire.
    static let grace: TimeInterval = 90

    // Reminder titles are personal data: log them `.private` so the unified log
    // redacts them unless a debug profile is installed.
    private static let log = Logger(subsystem: "com.ember.Ember", category: "reminders")

    public convenience init(source: any ReminderSource, client: APIClient, prefs: ReminderPrefs,
                            onRunningChange: @escaping @MainActor (Bool) -> Void = { _ in }) {
        self.init(source: source, prefs: prefs, send: Self.send(via: client),
                  now: { Date() }, sleep: { try await Task.sleep(for: $0) },
                  onRunningChange: onRunningChange)
    }

    /// Tests inject the send and the clocks.
    init(source: any ReminderSource, prefs: ReminderPrefs, send: @escaping Send,
         now: @escaping @MainActor () -> Date,
         sleep: @escaping @Sendable (Duration) async throws -> Void,
         onRunningChange: @escaping @MainActor (Bool) -> Void = { _ in }) {
        self.source = source
        self.prefs = prefs
        self.send = send
        self.now = now
        self.sleep = sleep
        self.onRunningChange = onRunningChange
    }

    /// Points fires at a new server (Connection change).
    public func configure(client: APIClient) {
        send = Self.send(via: client)
    }

    /// Starts the poll loop when enabled with access, stops it otherwise.
    public func sync() {
        let shouldRun = prefs.enabled && source.hasAccess
        if shouldRun, loop == nil {
            isRunning = true
            onRunningChange(true)
            loop = Task { [weak self] in
                while !Task.isCancelled {
                    guard let delay = await self?.pollAndWakeDelay(), let sleep = self?.sleep else { return }
                    try? await sleep(delay)
                }
            }
            Self.log.info("watcher started")
        } else if !shouldRun, let l = loop {
            l.cancel()
            loop = nil
            isRunning = false
            onRunningChange(false)
            Self.log.info("watcher stopped")
        }
    }

    private func pollAndWakeDelay() async -> Duration {
        let next = await poll()
        return wakeDelay(until: next)
    }

    /// Sleeps until the next reminder's fire time, so it rings on time rather
    /// than up to a poll interval late, capped so polling still discovers new
    /// reminders. Wakes 0.25 s past the fire time so that poll sees
    /// now ≥ fire time and fires on the first try.
    private func wakeDelay(until next: Date?) -> Duration {
        let cap = Self.pollInterval
        guard let next else { return .seconds(cap) }
        return .seconds(min(cap, max(0.5, next.timeIntervalSince(now()) + 0.25)))
    }

    /// Reads the source, refreshes `upcoming`, fires what is due, and returns
    /// the nearest future fire time (due − lead), or nil when there is none or
    /// the scheduler shouldn't run.
    @discardableResult
    func poll() async -> Date? {
        guard prefs.enabled, source.hasAccess else { return nil }
        let now = now()
        let lead = Double(prefs.leadMinutes) * 60
        let reminders = await source.dueTimedReminders()
        tracker.prune(now: now)
        Self.log.debug("poll fetched \(reminders.count) due-timed reminders")
        upcoming = reminders
            .filter { $0.due >= now }
            .sorted { $0.due < $1.due }
            .prefix(5).map { $0 }

        for r in reminders {
            let due = r.due
            guard reminderShouldFire(now: now, dueDate: due, leadMinutes: prefs.leadMinutes, grace: Self.grace) else { continue }
            let key = reminderDedupeKey(id: r.id, dueDate: due)
            let title = r.title.trimmingCharacters(in: .whitespacesAndNewlines)
            if title.isEmpty { continue }
            guard tracker.begin(key) else { continue }
            Self.log.info("firing \(title, privacy: .private) due=\(due, privacy: .public)")
            let outcome = await fire(title: title, key: key)
            tracker.finish(key, due: due, outcome: outcome)
        }

        return reminders
            .map { $0.due.addingTimeInterval(-lead) }
            .filter { $0 > now }
            .min()
    }

    /// Sends one occurrence and reports what the result proves about delivery.
    private func fire(title: String, key: String) async -> ReminderFireOutcome {
        let p = prefs
        let request = Fire(text: title, sound: p.sound, duration: p.popupDuration,
                           nativeIconId: p.useNativeIcon ? p.nativeIconId : "", hold: p.hold,
                           repeatSound: p.repeatSound, key: key)
        do {
            try await send(request)
            lastFireError = nil
            return .delivered
        } catch {
            let outcome = ReminderFireOutcome(error: error)
            lastFireError = error.localizedDescription
            Self.log.error("fire failed (\(outcome == .notDelivered ? "will retry" : "not retried", privacy: .public)): \(error.localizedDescription, privacy: .public)")
            return outcome
        }
    }

    private static func send(via client: APIClient) -> Send {
        let service = RemindersService(client: client)
        return { f in
            try await service.fire(text: f.text, sound: f.sound, duration: f.duration,
                                   nativeIconId: f.nativeIconId, hold: f.hold,
                                   repeatSound: f.repeatSound, key: f.key)
        }
    }
}
