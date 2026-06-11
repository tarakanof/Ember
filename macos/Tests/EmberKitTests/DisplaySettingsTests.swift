import Testing
import Foundation
@testable import EmberKit

@Test func readsDisplayDefaultsWhenAbsent() {
    let d = DisplaySettings(reading: EnvFile(parsing: ""))
    #expect(d.contextPct); #expect(d.activityDetail); #expect(d.activityTrail)
    #expect(d.sourceCard); #expect(d.sessionBar)
    #expect(!d.rateBottomBar)
}

@Test func readsDisplayExplicitValues() {
    let d = DisplaySettings(reading: EnvFile(parsing: """
    EMBER_CONTEXT_PCT_ENABLED=false
    EMBER_RATE_BOTTOM_BAR=on
    """))
    #expect(!d.contextPct)
    #expect(d.rateBottomBar)
}

@Test func appliesTogglesAsTrueFalse() {
    var env = EnvFile(parsing: "")
    var d = DisplaySettings(reading: EnvFile(parsing: ""))
    d.activityDetail = false
    d.apply(to: &env)
    #expect(env.get(SettingsKeys.activityDetail) == "false")
    #expect(env.get(SettingsKeys.contextPct) == "true")
}

@Test func applyDoesNotWriteRetiredKeys() {
    var env = EnvFile(parsing: "")
    let s = DisplaySettings(reading: env)
    s.apply(to: &env)
    #expect(env.get("EMBER_RATE_PCT_ENABLED").isEmpty)
    #expect(env.get("EMBER_CONTEXT_NUMBER_ENABLED").isEmpty)
    #expect(env.get("EMBER_RATE_RESET").isEmpty)
}

@Test func draftDisplayExcludesActivityTrailAndTakesColor() {
    var d = DisplaySettings(reading: EnvFile(parsing: ""))
    d.rateBottomBar = true
    let draft = d.draftDisplay(sourceColor: "#ff8800")
    #expect(draft.rateBottomBar)
    #expect(draft.contextPct)
    #expect(draft.sourceColor == "#ff8800")
}

@Test func sourceCardAndSessionBarDefaults() {
    let s = DisplaySettings(reading: EnvFile(parsing: ""))
    #expect(s.sourceCard)   // envTrue: default on
    #expect(s.sessionBar)
    let off = DisplaySettings(reading: EnvFile(parsing: "EMBER_SOURCE_CARD=false\nEMBER_SESSION_BAR=false"))
    #expect(!off.sourceCard)
    #expect(!off.sessionBar)
}

@Test func applyWritesSourceCardAndSessionBar() {
    var env = EnvFile(parsing: "")
    var s = DisplaySettings(reading: env)
    s.sourceCard = false
    s.sessionBar = false
    s.apply(to: &env)
    #expect(env.get(SettingsKeys.sourceCard) == "false")
    #expect(env.get(SettingsKeys.sessionBar) == "false")
}

@Test func bottomBarModeMapping() {
    var s = DisplaySettings(reading: EnvFile(parsing: ""))
    #expect(s.bottomBarMode == .session)
    s.bottomBarMode = .rate
    #expect(s.rateBottomBar); #expect(!s.sessionBar)
    s.bottomBarMode = .off
    #expect(!s.rateBottomBar); #expect(!s.sessionBar)
    s.bottomBarMode = .session
    #expect(!s.rateBottomBar); #expect(s.sessionBar)
}

@Test func draftDisplayCarriesNewParams() {
    let s = DisplaySettings(reading: EnvFile(parsing: "EMBER_SOURCE_CARD=false"))
    let items = s.draftDisplay(sourceColor: "").queryItems
    #expect(items.contains(URLQueryItem(name: "source_card", value: "false")))
    #expect(items.contains(URLQueryItem(name: "session_bar", value: "true")))
    #expect(items.contains(URLQueryItem(name: "usage_card", value: "true")))
}
