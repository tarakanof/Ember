import Testing
import Foundation
@testable import EmberKit

private let now = Date(timeIntervalSince1970: 1_800_000_000)

private func device(_ id: String, kind: String = KnobDevice.knobKind, name: String, luxAge: TimeInterval? = nil) -> KnobDevice {
    var d = KnobDevice(id: id, kind: kind, hwID: "a0b1c2" + id.suffix(6), name: name, createdAt: Date(timeIntervalSince1970: 0))
    if let luxAge {
        var c = KnobCheckin(seenAt: now, fw: "0.10.0", ip: "192.0.2.10", rssi: -58,
                            heapInternalFree: 0, heapInternalLargest: 0, uptimeS: 0, appliedVersion: 1)
        c.room = KnobRoomReading(lux: 312, receivedAt: now.addingTimeInterval(-luxAge))
        d.lastCheckin = c
    }
    return d
}

private let me = device("knob-aaaaaa", name: "Desk")
private let clock = device("clock-cccccc", kind: KnobDevice.clockKind, name: "Clock CCCCCC")
private let shelf = device("knob-bbbbbb", name: "Shelf", luxAge: 30)

@Test func brightnessSourcesListAutomaticTheClockAndOtherKnobsWithLux() {
    let got = KnobSettings.brightnessSources(for: me, in: [me, clock, shelf], current: nil, now: now)
    #expect(got == [.automatic, .clock, .knob(id: "knob-bbbbbb", name: "Shelf")])
}

@Test func brightnessSourcesNeverListTheClockAsADeviceRow() {
    let got = KnobSettings.brightnessSources(for: me, in: [me, clock, shelf], current: nil, now: now)
    #expect(!got.contains { $0.tag == clock.id })
}

@Test func brightnessSourcesHideWithoutAChoice() {
    #expect(KnobSettings.brightnessSources(for: me, in: [me, clock], current: nil, now: now).isEmpty)
    #expect(KnobSettings.brightnessSources(for: me, in: [me], current: nil, now: now).isEmpty)
    let stale = device("knob-bbbbbb", name: "Shelf", luxAge: KnobDevice.luxRecentWindow + 1)
    #expect(KnobSettings.brightnessSources(for: me, in: [me, clock, stale], current: nil, now: now).isEmpty)
}

@Test func brightnessSourcesOfferTheClockToAKnobWithItsOwnSensor() {
    let lit = device("knob-aaaaaa", name: "Desk", luxAge: 10)
    #expect(KnobSettings.brightnessSources(for: lit, in: [lit, clock], current: nil, now: now) == [.automatic, .clock])
}

@Test func brightnessSourcesKeepASavedSourceThatStoppedReporting() {
    let quiet = device("knob-bbbbbb", name: "Shelf")
    #expect(KnobSettings.brightnessSources(for: me, in: [me, quiet], current: "knob-bbbbbb", now: now)
        == [.automatic, .knob(id: "knob-bbbbbb", name: "Shelf")])
    #expect(KnobSettings.brightnessSources(for: me, in: [me], current: "knob-gone00", now: now)
        == [.automatic, .unavailable(id: "knob-gone00")])
}

@Test func brightnessSourceTagReadsTheClocksRegistryIDAsTheClock() {
    #expect(KnobSettings.brightnessSourceTag("clock-cccccc", in: [me, clock]) == "clock")
    #expect(KnobSettings.brightnessSourceTag("", in: [me, clock]) == nil)
    #expect(KnobSettings.brightnessSourceTag("knob-bbbbbb", in: [me, clock]) == "knob-bbbbbb")
}

@Test func clearingTheBrightnessSourceSendsNull() throws {
    var old = KnobSettings.defaults
    old.brightness.source = "clock"
    var new = old
    new.brightness.source = nil
    #expect(new.patch(from: old) == ["brightness": .object(["source": .null])])
    var set = KnobSettings.defaults
    set.brightness.source = "knob-bbbbbb"
    #expect(set.patch(from: .defaults) == ["brightness": .object(["source": .string("knob-bbbbbb")])])
}

@Test func checkinDecodesTheRoomLux() throws {
    let json = #"{"seen_at":"2026-10-30T12:00:00Z","fw":"0.10.0","ip":"192.0.2.10","rssi":-58,"heap_internal_free":0,"heap_internal_largest":0,"uptime_s":0,"applied_version":1,"room":{"lux":312,"t_c":21.5,"received_at":"2026-10-30T12:00:00Z"}}"#
    let dec = JSONDecoder()
    dec.dateDecodingStrategy = .iso8601
    let c = try dec.decode(KnobCheckin.self, from: Data(json.utf8))
    #expect(c.room?.lux == 312)
    #expect(c.room?.receivedAt == ISO8601DateFormatter().date(from: "2026-10-30T12:00:00Z"))
}
