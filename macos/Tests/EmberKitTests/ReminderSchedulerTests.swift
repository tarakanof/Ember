import Testing
import Foundation
@testable import EmberKit

/// In-memory `ReminderSource`; lock-guarded because the scheduler reads it off
/// the main actor.
private final class FakeSource: ReminderSource, @unchecked Sendable {
    private let lock = NSLock()
    private var _reminders: [DueReminder]
    private var _access = true
    private var _fetches = 0

    init(_ reminders: [DueReminder] = []) { _reminders = reminders }

    var reminders: [DueReminder] {
        get { lock.withLock { _reminders } }
        set { lock.withLock { _reminders = newValue } }
    }
    var access: Bool {
        get { lock.withLock { _access } }
        set { lock.withLock { _access = newValue } }
    }
    var fetches: Int { lock.withLock { _fetches } }

    var hasAccess: Bool { access }
    func dueTimedReminders() async -> [DueReminder] {
        lock.withLock { _fetches += 1; return _reminders }
    }
}

/// Records fire requests; throws the next scripted error, if any.
private final class Sent: @unchecked Sendable {
    private let lock = NSLock()
    private var _fires: [ReminderScheduler.Fire] = []
    private var _errors: [Error] = []

    var fires: [ReminderScheduler.Fire] { lock.withLock { _fires } }
    func failNext(_ e: Error) { lock.withLock { _errors.append(e) } }

    var send: ReminderScheduler.Send {
        { [self] f in
            let error: Error? = lock.withLock {
                _fires.append(f)
                return _errors.isEmpty ? nil : _errors.removeFirst()
            }
            if let error { throw error }
        }
    }
}

private let base = Date(timeIntervalSince1970: 1_000_000)
private let enabled = ReminderPrefs(enabled: true)

private func seconds(_ d: Duration) -> TimeInterval {
    Double(d.components.seconds) + Double(d.components.attoseconds) / 1e18
}

/// A scheduler whose wall clock is `base + clock.now`.
@MainActor
private func makeScheduler(_ source: FakeSource, _ sent: Sent, clock: ManualClock = ManualClock(),
                           prefs: ReminderPrefs = enabled,
                           onRunningChange: @escaping @MainActor (Bool) -> Void = { _ in }) -> ReminderScheduler {
    ReminderScheduler(source: source, prefs: prefs, send: sent.send,
                      now: { base.addingTimeInterval(seconds(clock.now)) },
                      sleep: clock.sleepFn, onRunningChange: onRunningChange)
}

/// A scheduler polled by hand at `now()`.
@MainActor
private func makeScheduler(_ source: FakeSource, _ sent: Sent, prefs: ReminderPrefs = enabled,
                           now: @escaping @MainActor () -> Date) -> ReminderScheduler {
    ReminderScheduler(source: source, prefs: prefs, send: sent.send, now: now,
                      sleep: { _ in })
}

// MARK: Timing (the loop, on the manual clock)

@MainActor @Test func firesJustAfterDueNotUpToAPollLate() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(100))])
    let sent = Sent()
    let s = makeScheduler(source, sent, clock: clock)
    s.sync()

    await clock.advance(by: .seconds(100))
    #expect(sent.fires.isEmpty)
    // Polls at 0, 30, 60, 90, then sleeps 10.25 s to land just past due.
    #expect(source.fetches == 4)
    await clock.advance(by: .milliseconds(300))
    #expect(sent.fires.map(\.text) == ["Walk"])
    s.prefs.enabled = false
}

@MainActor @Test func leadTimeFiresEarly() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(400))])
    let sent = Sent()
    var prefs = enabled
    prefs.leadMinutes = 5
    let s = makeScheduler(source, sent, clock: clock, prefs: prefs)
    s.sync()

    await clock.advance(by: .seconds(100))
    #expect(sent.fires.isEmpty)
    await clock.advance(by: .milliseconds(300))
    #expect(sent.fires.count == 1)
    s.prefs.enabled = false
}

@MainActor @Test func pollsEveryThirtySecondsWithNothingDue() async {
    let clock = ManualClock()
    let source = FakeSource()
    let s = makeScheduler(source, Sent(), clock: clock)
    s.sync()
    await clock.advance(by: .seconds(95))
    #expect(source.fetches == 4)   // 0, 30, 60, 90
    s.prefs.enabled = false
}

@MainActor @Test func disablingStopsTheLoopAndReportsIt() async {
    let clock = ManualClock()
    let source = FakeSource()
    var changes: [Bool] = []
    let s = makeScheduler(source, Sent(), clock: clock, prefs: ReminderPrefs(enabled: false)) { changes.append($0) }

    s.sync()
    #expect(!s.isRunning)
    s.prefs.enabled = true          // a prefs change syncs by itself
    #expect(s.isRunning)
    await clock.advance(by: .seconds(1))
    s.prefs.enabled = false
    #expect(!s.isRunning)
    let fetches = source.fetches
    await clock.advance(by: .seconds(120))
    #expect(source.fetches == fetches)
    #expect(changes == [true, false])
}

@MainActor @Test func losingAccessStopsFiringAndSyncStopsTheLoop() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(60))])
    let sent = Sent()
    var changes: [Bool] = []
    let s = makeScheduler(source, sent, clock: clock) { changes.append($0) }
    s.sync()
    await clock.advance(by: .seconds(1))
    source.access = false
    await clock.advance(by: .seconds(90))
    #expect(sent.fires.isEmpty)     // each poll checks access first
    s.sync()
    #expect(!s.isRunning)
    #expect(changes == [true, false])
}

@MainActor @Test func doesNotStartWithoutAccess() {
    let source = FakeSource()
    source.access = false
    let s = makeScheduler(source, Sent())
    s.sync()
    #expect(!s.isRunning)
}

// MARK: Selection and delivery (polled by hand)

@MainActor @Test func firesEachOccurrenceOnceAndARecurrenceAgain() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    let s = makeScheduler(source, sent) { now }

    await s.poll()
    now = base.addingTimeInterval(30)
    await s.poll()
    #expect(sent.fires.count == 1)

    // Completed; the next occurrence keeps the id but has a new due date.
    source.reminders = [DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(60))]
    now = base.addingTimeInterval(61)
    await s.poll()
    #expect(sent.fires.map(\.key) == ["a|1000000", "a|1000060"])
}

@MainActor @Test func retriesOnlyWhenNothingWasSent() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    let s = makeScheduler(source, sent) { now }

    sent.failNext(RequestNotSent(underlying: .transport("refused")))
    await s.poll()
    #expect(s.lastFireError != nil)
    now = base.addingTimeInterval(30)
    await s.poll()                    // retried inside the grace window
    #expect(s.lastFireError == nil)
    now = base.addingTimeInterval(60)
    await s.poll()
    #expect(sent.fires.count == 2)
    #expect(Set(sent.fires.map(\.key)).count == 1)   // same idempotency key
}

@MainActor @Test func aMaybeDeliveredFireIsNotRetried() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    let s = makeScheduler(source, sent) { now }

    sent.failNext(APIError.http(status: 502, body: ""))
    await s.poll()
    now = base.addingTimeInterval(30)
    await s.poll()
    #expect(sent.fires.count == 1)
    #expect(s.lastFireError != nil)
}

@MainActor @Test func aRetryStopsAtTheEndOfTheGraceWindow() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    let s = makeScheduler(source, sent) { now }
    sent.failNext(RequestNotSent(underlying: .transport("refused")))
    await s.poll()
    now = base.addingTimeInterval(91)
    await s.poll()
    #expect(sent.fires.count == 1)
}

@MainActor @Test func aLongOverdueReminderDoesNotFireOnLaunch() async {
    let source = FakeSource([
        DueReminder(id: "old", title: "Old", due: base.addingTimeInterval(-91)),
        DueReminder(id: "edge", title: "Edge", due: base.addingTimeInterval(-90)),
    ])
    let sent = Sent()
    let s = makeScheduler(source, sent) { base }
    await s.poll()
    #expect(sent.fires.map(\.text) == ["Edge"])
}

@MainActor @Test func blankTitlesAreSkippedAndTitlesTrimmed() async {
    let source = FakeSource([
        DueReminder(id: "blank", title: " \n", due: base),
        DueReminder(id: "walk", title: "  Walk \n", due: base),
    ])
    let sent = Sent()
    let s = makeScheduler(source, sent) { base }
    await s.poll()
    #expect(sent.fires.map(\.text) == ["Walk"])
}

@MainActor @Test func prefsShapeTheRequestAndQuietIsLeftToTheServer() async {
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    let prefs = ReminderPrefs(enabled: true, sound: true, leadMinutes: 0, popupDuration: 20,
                              useNativeIcon: false, nativeIconId: "99", hold: true, repeatSound: true)
    let s = makeScheduler(source, sent, prefs: prefs) { base }
    await s.poll()
    #expect(sent.fires == [ReminderScheduler.Fire(text: "Walk", sound: true, duration: 20, nativeIconId: "",
                                                  hold: true, repeatSound: true, key: "a|1000000")])

    source.reminders = [DueReminder(id: "b", title: "Run", due: base)]
    s.prefs.useNativeIcon = true
    s.prefs.hold = false
    await s.poll()
    #expect(sent.fires.last?.nativeIconId == "99")
    #expect(sent.fires.last?.hold == false)
}

@MainActor @Test func upcomingListsTheNextFiveNotYetDue() async {
    let dues: [TimeInterval] = [600, -30, 60, 300, 0, 120, 900, 3600]
    let source = FakeSource(dues.enumerated().map {
        DueReminder(id: "r\($0.offset)", title: "R\($0.offset)", due: base.addingTimeInterval($0.element))
    })
    let s = makeScheduler(source, Sent()) { base }
    await s.poll()
    #expect(s.upcoming.map(\.id) == ["r4", "r2", "r5", "r3", "r0"])
}

@MainActor @Test func pollReturnsTheNearestFutureFireTime() async {
    let source = FakeSource([
        DueReminder(id: "a", title: "A", due: base.addingTimeInterval(900)),
        DueReminder(id: "b", title: "B", due: base.addingTimeInterval(400)),
        DueReminder(id: "c", title: "C", due: base.addingTimeInterval(-10)),
    ])
    var prefs = enabled
    prefs.leadMinutes = 5
    let s = makeScheduler(source, Sent(), prefs: prefs) { base }
    let next = await s.poll()
    #expect(next == base.addingTimeInterval(100))
}

@MainActor @Test func pollDoesNothingWhileDisabled() async {
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    let s = makeScheduler(source, sent, prefs: ReminderPrefs(enabled: false)) { base }
    let next = await s.poll()
    #expect(next == nil)
    #expect(source.fetches == 0)
    #expect(sent.fires.isEmpty)
}

// MARK: Production send

@MainActor @Test func firesThroughTheServerWithTheIdempotencyKey() async throws {
    let due = Date(timeIntervalSince1970: Double(Int(Date().timeIntervalSince1970)))
    let client = stubbedClient { req in
        #expect(req.url?.path == "/v1/reminders/fire")
        #expect(req.value(forHTTPHeaderField: "Idempotency-Key") == "a|\(Int(due.timeIntervalSince1970))")
        let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let obj = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        #expect(obj?["text"] as? String == "Walk")
        #expect(obj?["repeat_sound"] as? Bool == true)
        return (okResponse(req.url!, status: 204), Data())
    }
    var prefs = enabled
    prefs.repeatSound = true
    let s = ReminderScheduler(source: FakeSource([DueReminder(id: "a", title: "Walk", due: due)]),
                              client: client, prefs: prefs)
    await s.poll()
    #expect(s.lastFireError == nil)
}
