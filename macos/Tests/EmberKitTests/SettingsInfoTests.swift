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

@Test(arguments: SettingsInfoRequirement.allCases)
func settingsInfoRequirementIsOneSentence(_ r: SettingsInfoRequirement) {
    #expect(sentences(r.text.key) == 1)
    #expect(r.text.key.hasSuffix("."))
}

@Test func settingsInfoPopoverSaysWhyOnlyWhileDisabled() {
    let info = SettingsInfo.knobStatsInterval
    #expect(info.popover(rowEnabled: true, requirement: .diagnosticsOn).map(\.key) == [info.detail.key])
    #expect(info.popover(rowEnabled: false, requirement: .diagnosticsOn).map(\.key)
            == [info.detail.key, SettingsInfoRequirement.diagnosticsOn.text.key])
    #expect(info.popover(rowEnabled: false, requirement: nil).map(\.key) == [info.detail.key])
}

#if canImport(AppKit)
import SwiftUI
import AppKit

@MainActor private final class EnabledLog { var seen: [String: Bool] = [:] }

private struct EnabledProbe: View {
    @Environment(\.isEnabled) private var isEnabled
    let name: String
    let log: EnabledLog
    var body: some View {
        log.seen[name] = isEnabled
        return Text(verbatim: name)
    }
}

/// The info button lives in a row that is often disabled; `staysEnabled`
/// must win over the row's `.disabled(true)`.
@MainActor @Test func staysEnabledOverridesADisabledRow() {
    let log = EnabledLog()
    let row = HStack {
        EnabledProbe(name: "control", log: log)
        EnabledProbe(name: "info", log: log).staysEnabled()
    }
    .disabled(true)
    let host = NSHostingView(rootView: row)
    host.frame = NSRect(x: 0, y: 0, width: 200, height: 40)
    host.layoutSubtreeIfNeeded()
    #expect(log.seen["control"] == false)
    #expect(log.seen["info"] == true)
}
#endif
