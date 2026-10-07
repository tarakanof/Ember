import Testing
import Foundation
@testable import EmberKit

private func checkin() -> KnobCheckin {
    var c = KnobCheckin(seenAt: Date(timeIntervalSince1970: 0), fw: "0.9.15", ip: "192.0.2.61", rssi: -67,
                        heapInternalFree: 47104, heapInternalLargest: 31744, uptimeS: 93784, appliedVersion: 7)
    c.wifi = KnobWifi(disconnects: 3, rssiMin: -79)
    c.diag = KnobDiag(heapInternalMin: 38912, heapLargestMin: 30720)
    return c
}

@Test func knobNowReadoutUsesTheCheckinWhenThereAreNoStats() {
    let r = KnobNowReadout(latest: nil, checkin: checkin())
    #expect(r == KnobNowReadout(rssi: -67, rssiMin: -79, heapFree: 47104, heapLargest: 31744,
                                heapMin: 38912, largestMin: 30720, uptimeS: 93784))
}

@Test func knobNowReadoutPrefersTheLatestStatsSample() {
    var s = KnobStats.Sample(t: Date(timeIntervalSince1970: 60))
    s.rssiDBm = -60
    s.heapInternalFreeBytes = 50000
    s.heapInternalLargestBytes = 40000
    s.uptimeSec = 93844
    let r = KnobNowReadout(latest: s, checkin: checkin())
    #expect(r.rssi == -60 && r.heapFree == 50000 && r.heapLargest == 40000 && r.uptimeS == 93844)
    #expect(r.rssiMin == -79 && r.heapMin == 38912 && r.largestMin == 30720)
}

@Test func knobNowReadoutDropsUnreadZeros() {
    var c = checkin()
    c.rssi = 0
    c.heapInternalFree = 0
    c.wifi = nil
    c.diag = nil
    let r = KnobNowReadout(latest: nil, checkin: c)
    #expect(r.rssi == nil && r.heapFree == nil && r.rssiMin == nil && r.heapMin == nil)
    #expect(KnobNowReadout(latest: nil, checkin: nil) == KnobNowReadout())
}
