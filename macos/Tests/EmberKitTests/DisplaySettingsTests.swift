import Testing
@testable import EmberKit

@Test func readsDisplayDefaultsWhenAbsent() {
    let d = DisplaySettings(reading: EnvFile(parsing: ""))
    #expect(d.contextPct); #expect(d.ratePct); #expect(d.activityDetail); #expect(d.activityTrail)
    #expect(!d.contextNumber); #expect(!d.rateBottomBar); #expect(!d.rateReset)
}

@Test func readsDisplayExplicitValues() {
    let d = DisplaySettings(reading: EnvFile(parsing: """
    EMBER_CONTEXT_PCT_ENABLED=false
    EMBER_CONTEXT_NUMBER_ENABLED=true
    EMBER_RATE_BOTTOM_BAR=on
    """))
    #expect(!d.contextPct)
    #expect(d.ratePct)
    #expect(d.contextNumber)
    #expect(d.rateBottomBar)
    #expect(!d.rateReset)
}

@Test func appliesAllSevenTogglesAsTrueFalse() {
    var env = EnvFile(parsing: "")
    var d = DisplaySettings(reading: EnvFile(parsing: ""))
    d.contextNumber = true
    d.activityDetail = false
    d.apply(to: &env)
    #expect(env.get(SettingsKeys.contextNumber) == "true")
    #expect(env.get(SettingsKeys.activityDetail) == "false")
    #expect(env.get(SettingsKeys.rateReset) == "false")
    #expect(env.get(SettingsKeys.contextPct) == "true")
}

@Test func draftDisplayExcludesActivityTrailAndTakesColor() {
    var d = DisplaySettings(reading: EnvFile(parsing: ""))
    d.contextNumber = true
    d.rateReset = true
    let draft = d.draftDisplay(sourceColor: "#ff8800")
    #expect(draft.contextNumber)
    #expect(draft.rateReset)
    #expect(draft.contextPct)
    #expect(draft.sourceColor == "#ff8800")
}
