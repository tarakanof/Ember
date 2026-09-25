import Testing
import Foundation
@testable import EmberKit

@Test func firesAtDueTimeAndWithinGrace() {
    let due = Date(timeIntervalSince1970: 1_000_000)
    #expect(reminderShouldFire(now: due, dueDate: due, leadMinutes: 0, grace: 90))
    #expect(reminderShouldFire(now: due.addingTimeInterval(60), dueDate: due, leadMinutes: 0, grace: 90))
}

@Test func doesNotFireBeforeOrLongAfter() {
    let due = Date(timeIntervalSince1970: 1_000_000)
    #expect(!reminderShouldFire(now: due.addingTimeInterval(-1), dueDate: due, leadMinutes: 0, grace: 90))
    #expect(!reminderShouldFire(now: due.addingTimeInterval(120), dueDate: due, leadMinutes: 0, grace: 90))
}

@Test func leadTimeFiresEarly() {
    let due = Date(timeIntervalSince1970: 1_000_000)
    #expect(reminderShouldFire(now: due.addingTimeInterval(-300), dueDate: due, leadMinutes: 5, grace: 90))
    #expect(!reminderShouldFire(now: due.addingTimeInterval(-360), dueDate: due, leadMinutes: 5, grace: 90))
}

@Test func dedupeKeyIsStablePerOccurrence() {
    let due = Date(timeIntervalSince1970: 1_000_000)
    #expect(reminderDedupeKey(id: "abc", dueDate: due) == reminderDedupeKey(id: "abc", dueDate: due))
    #expect(reminderDedupeKey(id: "abc", dueDate: due) != reminderDedupeKey(id: "abc", dueDate: due.addingTimeInterval(60)))
}

@Test func ledgerRemembersOnlyRecordedKeys() {
    var ledger = ReminderFiredLedger()
    let due = Date(timeIntervalSince1970: 1_000_000)
    #expect(!ledger.contains("a"))
    ledger.record("a", due: due)
    #expect(ledger.contains("a"))
    #expect(!ledger.contains("b"))
}

@Test func ledgerPrunesEntriesADayPastDue() {
    var ledger = ReminderFiredLedger()
    let now = Date(timeIntervalSince1970: 10_000_000)
    ledger.record("old", due: now.addingTimeInterval(-86_401))
    ledger.record("recent", due: now.addingTimeInterval(-3_600))
    ledger.prune(now: now)
    #expect(!ledger.contains("old"))
    #expect(ledger.contains("recent"))
    #expect(ledger.count == 1)
}

@Test func reminderPrefsRoundTrips() throws {
    let p = ReminderPrefs(enabled: true, sound: false, leadMinutes: 10, popupDuration: 12, useNativeIcon: true, nativeIconId: "9")
    let data = try JSONEncoder().encode(p)
    #expect(try JSONDecoder().decode(ReminderPrefs.self, from: data) == p)
}
