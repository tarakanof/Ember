import Testing
import Foundation
@testable import EmberKit

@Test func saveStateEquates() {
    #expect(SaveState.saved == SaveState.saved)
    #expect(SaveState.error("a") != SaveState.error("b"))
    #expect(SaveState.idle != SaveState.saving)
}

@MainActor
private final class Counter {
    private(set) var value = 0
    func bump() { value += 1 }
}

// On a manual clock: real sleeps made these flaky under CI load.
@MainActor @Test func debouncedWriterCoalescesRapidSchedules() async {
    let clock = ManualClock()
    let writer = DebouncedWriter(delay: .milliseconds(40), sleep: clock.sleepFn)
    let counter = Counter()
    for _ in 0..<5 {
        writer.schedule { await counter.bump() }
        await clock.advance(by: .milliseconds(10))
    }
    #expect(counter.value == 0)
    await clock.advance(by: .milliseconds(40))
    #expect(counter.value == 1)   // only the last schedule survives
}

@MainActor @Test func debouncedWriterRunsAfterQuietPeriod() async {
    let clock = ManualClock()
    let writer = DebouncedWriter(delay: .milliseconds(20), sleep: clock.sleepFn)
    let counter = Counter()
    writer.schedule { await counter.bump() }
    await clock.advance(by: .milliseconds(19))
    #expect(counter.value == 0)
    await clock.advance(by: .milliseconds(1))
    #expect(counter.value == 1)
}

@MainActor @Test func debouncedWriterCancelDropsThePendingRun() async {
    let clock = ManualClock()
    let writer = DebouncedWriter(delay: .milliseconds(20), sleep: clock.sleepFn)
    let counter = Counter()
    writer.schedule { await counter.bump() }
    writer.cancel()
    await clock.advance(by: .seconds(1))
    #expect(counter.value == 0)
}
