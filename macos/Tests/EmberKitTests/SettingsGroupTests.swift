import Testing
@testable import EmberKit

@Test func settingsGroupsStartExpanded() {
    #expect(SettingsGroup.allCases.allSatisfy { !$0.isCollapsed(in: "") })
}

@Test func collapsingAGroupStoresItAndExpandingRemovesIt() {
    let collapsed = SettingsGroup.knobCrashDumps.setCollapsed(true, in: "")
    #expect(SettingsGroup.knobCrashDumps.isCollapsed(in: collapsed))
    #expect(!SettingsGroup.knobStatus.isCollapsed(in: collapsed))
    #expect(SettingsGroup.knobCrashDumps.setCollapsed(false, in: collapsed) == "")
}

@Test func collapsedGroupsKeepOtherStoredIDs() {
    let stored = SettingsGroup.knobFirmware.setCollapsed(true, in: "clock.future")
    #expect(stored == "clock.future,knob.firmware")
    #expect(SettingsGroup.knobFirmware.setCollapsed(false, in: stored) == "clock.future")
}

@Test func settingsGroupIDsAreStable() {
    #expect(SettingsGroup.allCases.map(\.rawValue) == [
        "knob.status", "knob.firmware", "knob.diagnostics", "knob.crash-dumps",
        "knob.display", "knob.behavior", "knob.advanced",
    ])
}
