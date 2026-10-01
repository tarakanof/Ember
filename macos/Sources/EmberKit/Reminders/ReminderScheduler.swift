import Foundation
import Observation
import OSLog

/// Rings the clock when an Apple Reminder comes due.
@MainActor
@Observable
public final class ReminderScheduler {
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
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var lastPoll: (wall: Date, uptime: TimeInterval)?
    @ObservationIgnored private var loop: Task<Void, Never>?
    @ObservationIgnored private var tracker = ReminderFireTracker()
    var rememberedFires: Int { tracker.firedCount }

    static let pollInterval: TimeInterval = 30
    static let grace: TimeInterval = 90
    static let armLead: TimeInterval = 300
    static let sleepSlack: TimeInterval = 5

    private static let log = Logger(subsystem: "com.ember.Ember", category: "reminders")

    public convenience init(source: any ReminderSource, client: APIClient, prefs: ReminderPrefs,
                            onArmedChange: @escaping @MainActor (Bool) -> Void = { _ in }) {
        self.init(source: source, prefs: prefs, send: Self.send(via: client),
                  now: { Date() }, uptime: { ProcessInfo.processInfo.systemUptime },
                  sleep: { try await Task.sleep(for: $0) },
                  onArmedChange: onArmedChange)
    }

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

    private func setArmed(_ armed: Bool, generation gen: Int?) {
        guard gen == generation else { return }
        let armed = armed && isRunning
        guard armed != isArmed else { return }
        isArmed = armed
        Self.log.debug("armed=\(armed, privacy: .public)")
        onArmedChange(armed)
    }

    private func wakeDelay(until next: Date?) -> Duration {
        let cap = Self.pollInterval
        guard let next else { return .seconds(cap) }
        return .seconds(min(cap, max(0.5, next.timeIntervalSince(now()) + 0.25)))
    }

    struct PollResult {
        var next: Date?
        var retryPending = false
    }

    @discardableResult
    func poll(generation gen: Int? = nil) async -> PollResult {
        guard prefs.enabled, source.hasAccess else {
            lastPoll = nil
            return PollResult()
        }
        let now = now()
        let up = uptime()
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
