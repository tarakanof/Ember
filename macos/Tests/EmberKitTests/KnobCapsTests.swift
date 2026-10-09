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
