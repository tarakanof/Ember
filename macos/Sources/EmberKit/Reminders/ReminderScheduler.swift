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
/// from `onArmedChange`.
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
    @ObservationIgnored private let uptime: @MainActor () -> TimeInterval
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let onArmedChange: @MainActor (Bool) -> Void
    @ObservationIgnored private var isArmed = false
    /// Bumped on each start and stop; a poll from an earlier loop may not arm.
    @ObservationIgnored private var generation = 0
    /// Wall clock and uptime at the last poll, to tell a napped sleep from a
    /// system sleep.
    @ObservationIgnored private var lastPoll: (wall: Date, uptime: TimeInterval)?
    @ObservationIgnored private var loop: Task<Void, Never>?
    @ObservationIgnored private var tracker = ReminderFireTracker()
    /// Fired occurrences still remembered; tests check that polls prune them.
    var rememberedFires: Int { tracker.firedCount }

    /// Longest sleep between polls, so newly created reminders are seen.
    static let pollInterval: TimeInterval = 30
    /// How long after its fire time a reminder may still fire.
    static let grace: TimeInterval = 90
    /// How close the next fire time must be before the scheduler arms. App
    /// Nap has stretched 30 s sleeps past `grace`, so a short lead leaves an
    /// unarmed sleep that can overshoot the fire window; five minutes narrows
    /// that risk rather than removing it. The napped-poll catch-up in `poll`
    /// covers the rest.
    static let armLead: TimeInterval = 300
    /// Largest gap between wall-clock and uptime progress between two polls
    /// still treated as "the Mac stayed awake". App Nap can stretch an unarmed
    /// sleep past `grace`; uptime keeps running through a nap but pauses in
    /// system sleep, so when both clocks kept pace `poll` fires what came due
    /// since the last poll even past grace. After a system sleep it doesn't,
    /// so reminders still ring only while the Mac is awake.
    static let sleepSlack: TimeInterval = 5

    // Reminder titles are personal data: log them `.private` so the unified log
    // redacts them unless a debug profile is installed.
    private static let log = Logger(subsystem: "com.ember.Ember", category: "reminders")

    public convenience init(source: any ReminderSource, client: APIClient, prefs: ReminderPrefs,
                            onArmedChange: @escaping @MainActor (Bool) -> Void = { _ in }) {
        self.init(source: source, prefs: prefs, send: Self.send(via: client),
                  now: { Date() }, uptime: { ProcessInfo.processInfo.systemUptime },
                  sleep: { try await Task.sleep(for: $0) },
                  onArmedChange: onArmedChange)
    }

    /// Tests inject the send and the clocks.
    init(source: any ReminderSource, prefs: ReminderPrefs, send: @escaping Send,
         now: @escaping @MainActor () -> Date,
         uptime: @escaping @MainActor () -> TimeInterval,
         sleep: @escaping @Sendable (Duration) async throws -> Void,
         onArmedChange: @escaping @MainActor (Bool) -> Void = { _ in }) {
        self.source = source
        self.prefs = prefs
        self.send = send
        self.now = now
        self.uptime = uptime
        self.sleep = sleep
        self.onArmedChange = onArmedChange
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
            generation += 1
            lastPoll = nil
            let gen = generation
            loop = Task { [weak self] in
                while !Task.isCancelled {
                    guard let delay = await self?.pollAndWakeDelay(generation: gen), let sleep = self?.sleep else { return }
                    try? await sleep(delay)
                }
            }
            Self.log.info("watcher started")
        } else if !shouldRun, let l = loop {
            l.cancel()
            loop = nil
            isRunning = false
            setArmed(false, generation: generation)
            generation += 1
            Self.log.info("watcher stopped")
        }
    }

    private func pollAndWakeDelay(generation gen: Int) async -> Duration {
        let result = await poll(generation: gen)
        let near = result.next.map { $0.timeIntervalSince(now()) <= Self.armLead } ?? false
        setArmed(result.retryPending || near, generation: gen)
        return wakeDelay(until: result.next)
    }

    /// Reports the armed state on change: true while a fire is in progress, a
    /// failed fire awaits its retry, or the next fire time is within
    /// `armLead`. Only the current loop's polls count, so one still in flight
    /// after a stop (or a stop and restart) can't arm the scheduler.
    private func setArmed(_ armed: Bool, generation gen: Int?) {
        guard gen == generation else { return }
        let armed = armed && isRunning
        guard armed != isArmed else { return }
        isArmed = armed
        Self.log.debug("armed=\(armed, privacy: .public)")
        onArmedChange(armed)
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

    struct PollResult {
        /// The nearest future fire time (due − lead), if any.
        var next: Date?
        /// A fire failed without reaching the server and will be retried.
        var retryPending = false
    }

    /// Reads the source, refreshes `upcoming`, fires what is due, and returns
    /// the nearest future fire time plus whether a retry is pending. `next` is
    /// nil when there is none or the scheduler shouldn't run. `generation` is
    /// the calling loop's, so its fires arm only that loop.
    @discardableResult
    func poll(generation gen: Int? = nil) async -> PollResult {
        guard prefs.enabled, source.hasAccess else {
            // A later poll must not catch up across the gap.
            lastPoll = nil
            return PollResult()
        }
        let now = now()
        let up = uptime()
        // Napped rather than slept since the last poll: see `sleepSlack`.
        let catchUpFrom = lastPoll.flatMap { last in
            now.timeIntervalSince(last.wall) - (up - last.uptime) < Self.sleepSlack ? last.wall : nil
        }
        lastPoll = (now, up)
        let lead = Double(prefs.leadMinutes) * 60
        let reminders = await source.dueTimedReminders()
        tracker.prune(now: now)
        Self.log.debug("poll fetched \(reminders.count) due-timed reminders")
        upcoming = reminders
            .filter { $0.due >= now }
            .sorted { $0.due < $1.due }
            .prefix(5).map { $0 }

        var retryPending = false
        for r in reminders {
            let due = r.due
            let fireTime = due.addingTimeInterval(-lead)
            let inWindow = reminderShouldFire(now: now, dueDate: due, leadMinutes: prefs.leadMinutes, grace: Self.grace)
            let napped = catchUpFrom.map { fireTime > $0 && fireTime <= now } ?? false
            guard inWindow || napped else { continue }
            let key = reminderDedupeKey(id: r.id, dueDate: due)
            let title = r.title.trimmingCharacters(in: .whitespacesAndNewlines)
            if title.isEmpty { continue }
            guard tracker.begin(key) else { continue }
            if inWindow {
                Self.log.info("firing \(title, privacy: .private) due=\(due, privacy: .public)")
            } else {
                Self.log.info("firing \(title, privacy: .private) due=\(due, privacy: .public) \(Int(now.timeIntervalSince(fireTime)), privacy: .public) s late after a napped sleep")
            }
            setArmed(true, generation: gen)
            // A catch-up fire is past grace, so a failure is final.
            let outcome = await fire(title: title, key: key, retryable: inWindow)
            tracker.finish(key, due: due, outcome: outcome)
            if outcome == .notDelivered, inWindow { retryPending = true }
        }

        let next = reminders
            .map { $0.due.addingTimeInterval(-lead) }
            .filter { $0 > now }
            .min()
        return PollResult(next: next, retryPending: retryPending)
    }

    /// Sends one occurrence and reports what the result proves about delivery.
    private func fire(title: String, key: String, retryable: Bool) async -> ReminderFireOutcome {
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
            Self.log.error("fire failed (\(outcome == .notDelivered && retryable ? "will retry" : "not retried", privacy: .public)): \(error.localizedDescription, privacy: .public)")
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
