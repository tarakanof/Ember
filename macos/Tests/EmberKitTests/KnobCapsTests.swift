import Testing
import Foundation
@testable import EmberKit

private func knob(fw: String?, caps: KnobCaps? = nil) -> KnobDevice {
    var d = KnobDevice(id: "knob-61fc8c", hwID: "a0b1c261fc8c", name: "Knob", createdAt: Date(timeIntervalSince1970: 0),
                       effectiveCaps: caps)
    if let fw {
        d.lastCheckin = KnobCheckin(seenAt: Date(timeIntervalSince1970: 0), fw: fw, ip: "192.0.2.10", rssi: -58,
                                    heapInternalFree: 0, heapInternalLargest: 0, uptimeS: 0, appliedVersion: 1)
    }
    return d
}

@Test func knobGatesReadEffectiveCaps() {
    let caps = KnobCaps(pages: ["bot", "weather"], features: ["view_wait"], source: "reported")
    let k = knob(fw: "0.9.41", caps: caps)
    #expect(!k.supports(feature: KnobCaps.statsIntervals), "caps win over a fw that would pass the semver gate")
    #expect(!k.supports(page: KnobCaps.nowPlayingPage))
    #expect(!k.supports(page: "pomodoro"))
    #expect(k.supports(page: "weather"))
    #expect(k.supportedPages == ["bot", "weather"])
    #expect(AppCatalog.apps(.knob, supportedPages: k.supportedPages) == [.bot, .weather])

    let newer = knob(fw: "0.1.0", caps: KnobCaps(pages: ["bot", "nowplaying"], features: [KnobCaps.statsIntervals]))
    #expect(newer.supports(feature: KnobCaps.statsIntervals), "caps win over a fw below the semver gate")
    #expect(newer.supports(page: KnobCaps.nowPlayingPage))
}

@Test func knobGatesFallBackToFirmwareWithoutEffectiveCaps() {
    #expect(knob(fw: "0.7.0").supports(feature: KnobCaps.statsIntervals))
    #expect(!knob(fw: "0.6.9").supports(feature: KnobCaps.statsIntervals))
    #expect(!knob(fw: nil).supports(feature: KnobCaps.statsIntervals))
    #expect(!knob(fw: "0.9.41").supports(feature: "np_control"), "an unknown token has no semver fallback")
    #expect(knob(fw: "0.9.0").supports(page: KnobCaps.nowPlayingPage))
    #expect(!knob(fw: "0.8.9").supports(page: KnobCaps.nowPlayingPage))
    #expect(knob(fw: nil).supports(page: "pomodoro"))
    #expect(knob(fw: "0.9.0").supportedPages == ["bot", "pomodoro", "weather", "nowplaying"])
    #expect(knob(fw: "0.8.0").supportedPages == ["bot", "pomodoro", "weather"])
}

@Test func knobDeviceDecodesEffectiveCaps() throws {
    let json = #"""
    {"devices":[{"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"a0b1c261fc8c","name":"Knob",
    "created_at":"2026-10-01T00:00:00Z","config_version":3,"rotation_pending":false,"rotated_at":null,
    "last_checkin":null,
    "effective_caps":{"view":[1,1],"pages":["bot","pomodoro"],"features":["stats_intervals"],
    "limits":{"view_bytes":16383,"config_bytes":1024},"source":"reported"}},
    {"id":"knob-000001","kind":"cinder-knob","hw_id":"000000000001","name":"Old server",
    "created_at":"2026-10-01T00:00:00Z","config_version":1,"rotation_pending":false,"rotated_at":null,
    "last_checkin":null}]}
    """#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let list = try d.decode(KnobDeviceList.self, from: Data(json.utf8))
    let caps = try #require(list.devices[0].effectiveCaps)
    #expect(caps == KnobCaps(view: [1, 1], pages: ["bot", "pomodoro"], features: ["stats_intervals"],
                             limits: .init(viewBytes: 16383, configBytes: 1024), source: "reported"))
    #expect(list.devices[0].supports(feature: KnobCaps.statsIntervals))
    #expect(list.devices[1].effectiveCaps == nil)
}

@Test func knobDeviceListSurvivesMalformedEffectiveCaps() throws {
    let json = #"""
    {"devices":[{"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"a0b1c261fc8c","name":"Knob",
    "created_at":"2026-10-01T00:00:00Z","config_version":3,"rotation_pending":false,"rotated_at":null,
    "last_checkin":null,"effective_caps":{"view":"1..2","pages":"bot"}}]}
    """#
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    let list = try d.decode(KnobDeviceList.self, from: Data(json.utf8))
    #expect(list.devices.count == 1)
    #expect(list.devices[0].effectiveCaps == nil)
    #expect(list.devices[0].supports(page: "bot"), "falls back to the semver gates")
}

@Test func knobDeviceDecodesCapsError() throws {
    let json = #"{"view":[1,1],"pages":["bot"],"features":[],"source":"legacy","caps_error":"page \"bot\" listed twice"}"#
    let caps = try JSONDecoder().decode(KnobCaps.self, from: Data(json.utf8))
    #expect(caps.capsError == #"page "bot" listed twice"#)
}

@Test func knobFirmwareFallbackSortsPreReleasesBelowTheirRelease() {
    #expect(!KnobDevice.firmware("0.9.0-rc.1", atLeast: [0, 9, 0]), "matches the server's legacy table")
    #expect(KnobDevice.firmware("0.9.0+local", atLeast: [0, 9, 0]), "build metadata is ignored")
    #expect(KnobDevice.firmware("0.9.1-rc.1", atLeast: [0, 9, 0]))
    #expect(!knob(fw: "0.9.0-rc.1").supports(page: KnobCaps.nowPlayingPage))
}

@Test func knobRotationChoicesFollowCaps() {
    let both = knob(fw: "0.10.0", caps: KnobCaps(pages: ["bot"], rotations: [180, 0]))
    #expect(both.supportedRotations == [0, 180])
    #expect(both.rotationChoices(current: 0) == [0, 180])
    #expect(both.rotationChoices(current: 90) == [0, 90, 180], "a stored value outside caps stays selectable")
    #expect(both.rotationChoices(current: nil).isEmpty, "a server that sends no rotation hides the picker")

    let all = knob(fw: "0.10.0", caps: KnobCaps(pages: ["bot"], rotations: [0, 90, 180, 270]))
    #expect(all.rotationChoices(current: 0) == [0, 90, 180, 270])

    #expect(knob(fw: "0.10.0", caps: KnobCaps(pages: ["bot"])).rotationChoices(current: 0).isEmpty)
    #expect(knob(fw: "0.10.0", caps: KnobCaps(pages: ["bot"], rotations: [0])).rotationChoices(current: 0).isEmpty)
    #expect(knob(fw: "0.10.0", caps: KnobCaps(pages: ["bot"], rotations: [])).rotationChoices(current: 0).isEmpty)
    #expect(knob(fw: "0.9.41").rotationChoices(current: 0).isEmpty, "no effective_caps means [0]")
    #expect(knob(fw: "0.10.0", caps: KnobCaps(pages: ["bot"], rotations: [45, 0])).supportedRotations == [0])
}

@Test func knobCapsDecodeRotations() throws {
    let with = try JSONDecoder().decode(KnobCaps.self, from: Data(#"{"view":[1,1],"pages":["bot"],"features":[],"rotations":[0,180],"source":"reported"}"#.utf8))
    #expect(with.rotations == [0, 180])
    let without = try JSONDecoder().decode(KnobCaps.self, from: Data(#"{"view":[1,1],"pages":["bot"],"features":[],"source":"legacy"}"#.utf8))
    #expect(without.rotations == nil)
}
