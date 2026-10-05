import Testing
@testable import EmberKit

@Suite struct KnobResetReasonTests {
    @Test func everyIDFNameHasWords() {
        #expect(KnobResetReason.known.count == 16)
        for raw in KnobResetReason.known {
            #expect(KnobResetReason.label(raw) != raw, "\(raw) shows raw")
        }
    }

    @Test func aNameFromNewerFirmwareShowsAsReported() {
        #expect(KnobResetReason.label("future_reason") == "future_reason")
    }
}
