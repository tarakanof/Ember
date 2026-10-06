import Testing
import Foundation
@testable import EmberKit

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

@MainActor
private func makeScheduler(_ source: any ReminderSource, _ sent: Sent, clock: ManualClock = ManualClock(),
                           prefs: ReminderPrefs = enabled,
                           onArmedChange: @escaping @MainActor (Bool) -> Void = { _ in }) -> ReminderScheduler {
    ReminderScheduler(source: source, prefs: prefs, send: sent.send,
                      now: { base.addingTimeInterval(seconds(clock.now)) },
                      uptime: { seconds(clock.now) },
                      sleep: clock.sleepFn, onArmedChange: onArmedChange)
}

@MainActor
private func makeScheduler(_ source: FakeSource, _ sent: Sent, prefs: ReminderPrefs = enabled,
                           uptime: (@MainActor () -> TimeInterval)? = nil,
                           now: @escaping @MainActor () -> Date) -> ReminderScheduler {
    ReminderScheduler(source: source, prefs: prefs, send: sent.send, now: now,
                      uptime: uptime ?? { now().timeIntervalSince(base) },
                      sleep: { _ in })
}

private final class GatedSource: ReminderSource, @unchecked Sendable {
    private let lock = NSLock()
    private let first: [DueReminder]
    private var fetches = 0
    private var opened = false
    private var waiter: CheckedContinuation<Void, Never>?

    init(first: [DueReminder]) { self.first = first }

    var hasAccess: Bool { true }
    func dueTimedReminders() async -> [DueReminder] {
        let isFirst = lock.withLock { fetches += 1; return fetches == 1 }
        guard isFirst else { return [] }
        await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
            let ready: Bool = lock.withLock {
                if opened { return true }
                waiter = c
                return false
            }
            if ready { c.resume() }
        }
        return first
    }

    func open() {
        let c: CheckedContinuation<Void, Never>? = lock.withLock {
            opened = true
            let w = waiter
            waiter = nil
            return w
        }
        c?.resume()
    }
}

@MainActor @Test func firesJustAfterDueNotUpToAPollLate() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(100))])
    let sent = Sent()
    let s = makeScheduler(source, sent, clock: clock)
    s.sync()

    await clock.advance(by: .seconds(100))
    #expect(sent.fires.isEmpty)
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
    #expect(source.fetches == 4)
    s.prefs.enabled = false
}

@MainActor @Test func disablingStopsTheLoop() async {
    let clock = ManualClock()
    let source = FakeSource()
    let s = makeScheduler(source, Sent(), clock: clock, prefs: ReminderPrefs(enabled: false))

    s.sync()
    #expect(!s.isRunning)
    s.prefs.enabled = true
    #expect(s.isRunning)
    await clock.advance(by: .seconds(1))
    s.prefs.enabled = false
    #expect(!s.isRunning)
    let fetches = source.fetches
    await clock.advance(by: .seconds(120))
    #expect(source.fetches == fetches)
}

@MainActor @Test func losingAccessStopsFiringAndSyncStopsTheLoop() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(60))])
    let sent = Sent()
    let s = makeScheduler(source, sent, clock: clock)
    s.sync()
    await clock.advance(by: .seconds(1))
    source.access = false
    await clock.advance(by: .seconds(90))
    #expect(sent.fires.isEmpty)
    s.sync()
    #expect(!s.isRunning)
}

@MainActor @Test func doesNotStartWithoutAccess() {
    let source = FakeSource()
    source.access = false
    let s = makeScheduler(source, Sent())
    s.sync()
    #expect(!s.isRunning)
}

@MainActor @Test func armsOnlyOnceTheNextFireIsWithinFiveMinutes() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(600))])
    let sent = Sent()
    var changes: [Bool] = []
    let s = makeScheduler(source, sent, clock: clock) { changes.append($0) }
    s.sync()

    await clock.advance(by: .seconds(299))
    #expect(changes.isEmpty)
    await clock.advance(by: .seconds(1))
    #expect(changes == [true])
    s.prefs.enabled = false
}

@MainActor @Test func releasesAfterTheFireWhenNothingElseIsNear() async {
    let clock = ManualClock()
    let source = FakeSource([
        DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(60)),
        DueReminder(id: "b", title: "Run", due: base.addingTimeInterval(3600)),
    ])
    let sent = Sent()
    var changes: [Bool] = []
    let s = makeScheduler(source, sent, clock: clock) { changes.append($0) }
    s.sync()

    await clock.advance(by: .seconds(61))
    #expect(sent.fires.map(\.text) == ["Walk"])
    #expect(changes == [true, false])
    s.prefs.enabled = false
    #expect(changes == [true, false])
}

@MainActor private final class ArmedLog {
    var changes: [Bool] = []
    var armed: Bool { changes.last ?? false }
}

@MainActor @Test func holdsWhileAFireIsInProgress() async {
    let clock = ManualClock()
    let log = ArmedLog()
    let armedAtSend = ArmedLog()
    let s = ReminderScheduler(source: FakeSource([DueReminder(id: "a", title: "Walk", due: base)]),
                              prefs: enabled,
                              send: { _ in await MainActor.run { armedAtSend.changes.append(log.armed) } },
                              now: { base.addingTimeInterval(seconds(clock.now)) },
                              uptime: { seconds(clock.now) },
                              sleep: clock.sleepFn) { log.changes.append($0) }
    s.sync()

    await clock.settle()
    #expect(armedAtSend.changes == [true])
    #expect(log.changes == [true, false])
    s.prefs.enabled = false
}

@MainActor @Test func staysArmedUntilAFailedFireIsRetried() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    var changes: [Bool] = []
    let s = makeScheduler(source, sent, clock: clock) { changes.append($0) }
    sent.failNext(RequestNotSent(underlying: .transport("refused")))
    s.sync()

    await clock.settle()
    #expect(sent.fires.count == 1)
    #expect(changes == [true])
    await clock.advance(by: .seconds(30))
    #expect(sent.fires.count == 2)
    #expect(changes == [true, false])
    s.prefs.enabled = false
}

@MainActor @Test func aPollFromAStoppedLoopCannotArmItsSuccessor() async {
    let clock = ManualClock()
    let source = GatedSource(first: [DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(30))])
    var changes: [Bool] = []
    let s = makeScheduler(source, Sent(), clock: clock) { changes.append($0) }
    s.sync()
    await clock.settle()
    s.prefs.enabled = false
    s.prefs.enabled = true
    await clock.settle()

    source.open()
    await clock.settle()
    #expect(changes.isEmpty)
    s.prefs.enabled = false
}

@MainActor @Test func stoppingReleasesAnArmedHold() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(30))])
    var changes: [Bool] = []
    let s = makeScheduler(source, Sent(), clock: clock) { changes.append($0) }
    s.sync()

    await clock.settle()
    #expect(changes == [true])
    s.prefs.enabled = false
    #expect(changes == [true, false])
}

@MainActor @Test func aLateWakeStillFiresWhatCameDueDuringTheSleep() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(200))])
    let sent = Sent()
    let s = makeScheduler(source, sent) { now }
    await s.poll()
    now = base.addingTimeInterval(270)
    await s.poll()
    #expect(sent.fires.map(\.text) == ["Walk"])
}

@MainActor @Test func aNappedSleepPastGraceStillFires() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(200))])
    let sent = Sent()
    let s = makeScheduler(source, sent) { now }
    await s.poll()
    now = base.addingTimeInterval(400)
    await s.poll()
    #expect(sent.fires.map(\.text) == ["Walk"])
    now = base.addingTimeInterval(430)
    await s.poll()
    #expect(sent.fires.count == 1)
}

@MainActor @Test func regainingAccessDoesNotCatchUpAcrossTheGap() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(200))])
    let sent = Sent()
    let s = makeScheduler(source, sent) { now }
    await s.poll()
    source.access = false
    now = base.addingTimeInterval(3600)
    await s.poll()
    source.access = true
    now = base.addingTimeInterval(3630)
    await s.poll()
    #expect(sent.fires.isEmpty)
}

@MainActor @Test func aRestartDoesNotCatchUpAcrossTheStop() async {
    let clock = ManualClock()
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(200))])
    let sent = Sent()
    let s = makeScheduler(source, sent, clock: clock)
    s.sync()
    await clock.advance(by: .seconds(1))
    s.prefs.enabled = false
    await clock.advance(by: .seconds(400))
    s.prefs.enabled = true
    await clock.settle()
    #expect(sent.fires.isEmpty)
    s.prefs.enabled = false
}

@MainActor @Test func aSystemSleepPastGraceDoesNotFire() async {
    var now = base
    var uptime: TimeInterval = 0
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base.addingTimeInterval(200))])
    let sent = Sent()
    let s = makeScheduler(source, sent, uptime: { uptime }) { now }
    await s.poll()
    now = base.addingTimeInterval(400)
    uptime = 10
    await s.poll()
    #expect(sent.fires.isEmpty)
}

@MainActor @Test func firesEachOccurrenceOnceAndARecurrenceAgain() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    let s = makeScheduler(source, sent) { now }

    await s.poll()
    now = base.addingTimeInterval(30)
    await s.poll()
    #expect(sent.fires.count == 1)

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
    await s.poll()
    #expect(s.lastFireError == nil)
    now = base.addingTimeInterval(60)
    await s.poll()
    #expect(sent.fires.count == 2)
    #expect(Set(sent.fires.map(\.key)).count == 1)
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
    let next = await s.poll().next
    #expect(next == base.addingTimeInterval(100))
}

@MainActor @Test func pollDoesNothingWhileDisabled() async {
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let sent = Sent()
    let s = makeScheduler(source, sent, prefs: ReminderPrefs(enabled: false)) { base }
    let next = await s.poll().next
    #expect(next == nil)
    #expect(source.fetches == 0)
    #expect(sent.fires.isEmpty)
}

@MainActor @Test func firesThroughTheServerWithTheIdempotencyKey() async throws {
    let due = Date(timeIntervalSince1970: Double(Int(Date().timeIntervalSince1970)))
    let calls = Counter()
    let client = stubbedClient { req in
        calls.increment()
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
    #expect(calls.value == 1)
    #expect(s.lastFireError == nil)
}

private final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var n = 0
    var value: Int { lock.withLock { n } }
    func increment() { lock.withLock { n += 1 } }
}

@MainActor @Test func aPollForgetsOccurrencesADayPastDue() async {
    var now = base
    let source = FakeSource([DueReminder(id: "a", title: "Walk", due: base)])
    let s = makeScheduler(source, Sent()) { now }
    await s.poll()
    #expect(s.rememberedFires == 1)
    now = base.addingTimeInterval(86_400)
    await s.poll()
    #expect(s.rememberedFires == 1)
    now = base.addingTimeInterval(86_401)
    await s.poll()
    #expect(s.rememberedFires == 0)
}
