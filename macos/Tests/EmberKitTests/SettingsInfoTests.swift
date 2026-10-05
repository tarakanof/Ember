import Testing
import Foundation
@testable import EmberKit

private func sentences(_ s: String) -> Int {
    s.matches(of: /[.!?](\s|$)/).count
}

@Test(arguments: SettingsInfo.allCases)
func settingsInfoSummaryIsOneShortLine(_ info: SettingsInfo) {
    let s = info.summary.key
    #expect(!s.isEmpty)
    #expect(s.count <= 80, "\(info): tooltip too long (\(s.count))")
    #expect(!s.contains("\n"))
    #expect(!s.hasSuffix("."), "\(info): a tooltip is a phrase, without a full stop")
}

@Test(arguments: SettingsInfo.allCases)
func settingsInfoDetailIsOneToThreeSentences(_ info: SettingsInfo) {
    let d = info.detail.key
    #expect((1...3).contains(sentences(d)), "\(info): \(sentences(d)) sentences")
    #expect(d.count <= 330, "\(info): popover text too long (\(d.count))")
    #expect(d.hasSuffix("."))
    #expect(d != info.summary.key)
}

@Test func settingsInfoSummariesAreDistinct() {
    let summaries = SettingsInfo.allCases.map(\.summary.key)
    #expect(Set(summaries).count == summaries.count)
}

@Test func settingsInfoSentenceCounter() {
    #expect(sentences("One. Two. Default: 4 %.") == 3)
    #expect(sentences("A 0.5 s step.") == 1)
}
