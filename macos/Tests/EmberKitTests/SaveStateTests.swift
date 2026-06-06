import Testing
import Foundation
@testable import EmberKit

@Test func saveStateEquates() {
    #expect(SaveState.saved == SaveState.saved)
    #expect(SaveState.error("a") != SaveState.error("b"))
    #expect(SaveState.idle != SaveState.saving)
}

private actor Counter {
    private(set) var value = 0
    func bump() { value += 1 }
}

@Test func debouncedWriterCoalescesRapidSchedules() async {
    let writer = await DebouncedWriter(delay: .milliseconds(40))
    let counter = Counter()
    for _ in 0..<5 { await writer.schedule { await counter.bump() } }
    try? await Task.sleep(for: .milliseconds(200))
    #expect(await counter.value == 1)   // only the last schedule survives
}

@Test func debouncedWriterRunsAfterQuietPeriod() async {
    let writer = await DebouncedWriter(delay: .milliseconds(20))
    let counter = Counter()
    await writer.schedule { await counter.bump() }
    try? await Task.sleep(for: .milliseconds(120))
    #expect(await counter.value == 1)
}
