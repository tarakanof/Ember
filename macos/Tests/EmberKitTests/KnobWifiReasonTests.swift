import Testing
@testable import EmberKit

@Suite struct KnobWifiReasonTests {
    static let worded = [1, 2, 3, 4, 5, 6, 7, 8, 14, 15, 16, 23, 34, 39,
                         200, 201, 202, 203, 204, 205, 206, 207, 208, 209, 210, 211, 212]

    @Test func exactlyTheWordedCodesHaveWords() {
        #expect((0...255).filter { KnobWifiReason.label($0) != nil } == Self.worded)
    }

    @Test func labelsReadAsTheIDFMeaning() {
        #expect(KnobWifiReason.label(203) == "association failed")
        #expect(KnobWifiReason.label(15) == "4-way handshake timeout")
        #expect(KnobWifiReason.label(3) == "access point left")
        #expect(KnobWifiReason.label(212) == "no access point above the signal threshold")
    }

    @Test func aReasonThisAppDoesNotKnowHasNoWords() {
        #expect(KnobWifiReason.label(0) == nil)
        #expect(KnobWifiReason.label(250) == nil)
    }
}
