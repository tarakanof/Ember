import Testing
import Foundation
@testable import EmberKit

@Test func paneNamesAreTheContract() {
    #expect(SettingsPaneID.allCases.map(\.rawValue) ==
            ["general", "connection", "clock", "agents", "focus", "weather", "calendar", "sounds"])
    #expect(SettingsPaneID.storageKey == "settings.pane")
}

@Test func legacyPaneNamesMigrate() {
    #expect(SettingsPaneID(stored: "app") == .general)
    #expect(SettingsPaneID(stored: "device") == .clock)
    #expect(SettingsPaneID(stored: "display") == .agents)
    #expect(SettingsPaneID(stored: "pomodoro") == .focus)
    #expect(SettingsPaneID(stored: "meetings") == .calendar)
    #expect(SettingsPaneID(stored: "reminders") == .calendar)
    #expect(SettingsPaneID(stored: "sounds") == .sounds)
    #expect(SettingsPaneID(stored: "nonsense") == .connection)
    #expect(SettingsPaneID(stored: nil) == .connection)
}

@Test func melodyChoiceFromValue() {
    let names = ["bell", "chime"]
    #expect(MelodyChoice(value: "", available: names) == .builtIn)
    #expect(MelodyChoice(value: "  ", available: names) == .builtIn)
    #expect(MelodyChoice(value: "bell", available: names) == .stored("bell"))
    #expect(MelodyChoice(value: "x:d=4:c", available: names) == .custom)
    // A name the clock no longer has stays as typed rather than vanishing.
    #expect(MelodyChoice(value: "gone", available: names) == .custom)
}

@Test func melodyChoiceToValue() {
    let names = ["bell"]
    #expect(MelodyChoice.builtIn.value(replacing: "bell", available: names) == "")
    #expect(MelodyChoice.stored("bell").value(replacing: "", available: names) == "bell")
    #expect(MelodyChoice.custom.value(replacing: "x:d=4:c", available: names) == "x:d=4:c")
    #expect(MelodyChoice.custom.value(replacing: "bell", available: names) == "")
}
