import Testing
@testable import EmberKit

// The placeholder marker is gone now that real types exist; this smoke test
// just confirms the public surface links.
@Test func publicSurfaceLinks() {
    #expect(appIconPalettes.count == 2)
    #expect(PomodoroAction.allCases.count == 5)
    #expect(MenuPrefs.default.appIcon == "spark")
}
