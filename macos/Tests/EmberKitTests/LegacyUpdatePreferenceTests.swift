import Foundation
import Testing
@testable import EmberKit

private func scratchDefaults() -> UserDefaults {
    UserDefaults(suiteName: "legacy-update-\(UUID().uuidString)")!
}

@Suite struct LegacyUpdatePreferenceTests {
    @Test func turnedOffCarriesOverOnce() {
        let d = scratchDefaults()
        d.set(false, forKey: "updates.checkAutomatically")
        #expect(LegacyUpdatePreference.take(from: d) == false)
        #expect(LegacyUpdatePreference.take(from: d) == nil)
    }

    @Test func turnedOnCarriesOver() {
        let d = scratchDefaults()
        d.set(true, forKey: "updates.checkAutomatically")
        #expect(LegacyUpdatePreference.take(from: d) == true)
    }

    @Test func neverSetLeavesSparklesDefault() {
        #expect(LegacyUpdatePreference.take(from: scratchDefaults()) == nil)
    }

    @Test func retiredKeysAreDeleted() {
        let d = scratchDefaults()
        d.set(true, forKey: "updates.checkAutomatically")
        d.set(Date(), forKey: "updates.lastAttempt")
        d.set(Data("{}".utf8), forKey: "updates.lastRelease")
        d.set("keep", forKey: "producers.lastReconciledVersion")
        _ = LegacyUpdatePreference.take(from: d)
        #expect(d.object(forKey: "updates.checkAutomatically") == nil)
        #expect(d.object(forKey: "updates.lastAttempt") == nil)
        #expect(d.object(forKey: "updates.lastRelease") == nil)
        #expect(d.string(forKey: "producers.lastReconciledVersion") == "keep")
    }

    @Test func aNonBoolValueIsDroppedNotCarried() {
        let d = scratchDefaults()
        d.set("yes", forKey: "updates.checkAutomatically")
        #expect(LegacyUpdatePreference.take(from: d) == nil)
        #expect(d.object(forKey: "updates.checkAutomatically") == nil)
    }
}
