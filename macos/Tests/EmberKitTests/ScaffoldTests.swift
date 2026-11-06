import Testing
@testable import EmberKit

@Test func publicSurfaceLinks() {
    #expect(appIconPalettes.count == 3)
    #expect(PomodoroAction.allCases.count == 5)
    #expect(MenuPrefs.default.appIcon == "bot")
}
