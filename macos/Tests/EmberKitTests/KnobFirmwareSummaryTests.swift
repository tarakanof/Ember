import Testing
@testable import EmberKit

private let running = KnobOTAStatus.Running(fw: "0.9.15", rollback: true)

@Test func firmwareSummaryNamesAnAvailableUpdate() {
    #expect(KnobOTAStatus(running: running, available: "0.9.16").summary == .updateAvailable("0.9.16"))
    #expect(KnobOTAStatus(running: running).summary == .current)
}

@Test func firmwareSummaryShowsProgressWhileBusy() {
    #expect(KnobOTAStatus(target: "0.9.16", phase: .downloading, progressPct: 42).summary == .updating(percent: 42))
    #expect(KnobOTAStatus(target: "0.9.16", phase: .installing).summary == .updating(percent: nil))
}

@Test func firmwareSummaryReportsAFailure() {
    #expect(KnobOTAStatus(phase: .failed, error: "net", available: "0.9.16", version: "0.9.16").summary == .failed("0.9.16"))
    #expect(KnobOTAStatus(phase: .rolledBack, error: "boot", version: "0.9.16").summary == .failed("0.9.16"))
}

@Test func firmwareGroupExpandsWhenAnUpdateStartsOrFails() {
    let idle = KnobOTAStatus(running: running, available: "0.9.16")
    let busy = KnobOTAStatus(target: "0.9.16", phase: .downloading, progressPct: 10)
    let failed = KnobOTAStatus(phase: .failed, error: "net", version: "0.9.16")
    #expect(KnobOTAStatus.expandsGroup(from: idle, to: busy))
    #expect(KnobOTAStatus.expandsGroup(from: busy, to: failed))
    #expect(!KnobOTAStatus.expandsGroup(from: busy, to: busy))
    #expect(!KnobOTAStatus.expandsGroup(from: failed, to: failed))
    #expect(!KnobOTAStatus.expandsGroup(from: busy, to: idle))
    #expect(!KnobOTAStatus.expandsGroup(from: nil, to: failed))
    #expect(!KnobOTAStatus.expandsGroup(from: nil, to: busy))
}
