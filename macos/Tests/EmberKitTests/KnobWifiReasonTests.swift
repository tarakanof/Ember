import Testing
@testable import EmberKit

@Suite struct KnobWifiReasonTests {
    @Test func everyKnownReasonHasWords() {
        for code in KnobWifiReason.known {
            #expect(KnobWifiReason.label(code) != nil, "\(code) has no words")
        }
        #expect(KnobWifiReason.label(203) == "association failed")
        #expect(KnobWifiReason.label(15) == "4-way handshake timeout")
    }

    @Test func aReasonThisAppDoesNotKnowHasNoWords() {
        #expect(KnobWifiReason.label(0) == nil)
        #expect(KnobWifiReason.label(250) == nil)
    }
}
