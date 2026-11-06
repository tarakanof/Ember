import Testing
import Foundation
@testable import EmberKit

@Test func throttleDoesNotDemoteTheProxy() {
    var poller = MirrorPoller()
    #expect(poller.probesProxy)
    poller.record(.throttled(.seconds(2)))
    _ = poller.endTick(havePixels: false)
    #expect(poller.probesProxy)
}

@Test func proxyFailureDemotesUntilTheNextReprobe() {
    var poller = MirrorPoller(reprobeEvery: 4)
    poller.record(.failed)
    _ = poller.endTick(havePixels: true)
    #expect(!poller.probesProxy)
    _ = poller.endTick(havePixels: true)
    #expect(!poller.probesProxy)
    _ = poller.endTick(havePixels: true)
    #expect(!poller.probesProxy)
    _ = poller.endTick(havePixels: true)
    #expect(poller.probesProxy)
}

@Test func proxySuccessRestoresTheProxy() {
    var poller = MirrorPoller(reprobeEvery: 4)
    poller.record(.failed)
    _ = poller.endTick(havePixels: true)
    #expect(!poller.probesProxy)
    _ = poller.endTick(havePixels: true)
    _ = poller.endTick(havePixels: true)
    _ = poller.endTick(havePixels: true)
    poller.record(.pixels)
    _ = poller.endTick(havePixels: true)
    #expect(poller.probesProxy)
}

@Test func directReadIsUsedWheneverThereAreNoPixels() {
    var poller = MirrorPoller()
    poller.record(.throttled(.seconds(5)))
    #expect(poller.triesDirect(havePixels: false))

    var other = MirrorPoller()
    other.record(.failed)
    #expect(other.triesDirect(havePixels: false))
}

@Test func directReadIsSkippedOncePixelsAreInHand() {
    var poller = MirrorPoller()
    poller.record(.pixels)
    #expect(!poller.triesDirect(havePixels: true))
}

@Test func cadenceIsTheBaseWhilePixelsArrive() {
    var poller = MirrorPoller(cadence: .seconds(1))
    poller.record(.pixels)
    #expect(poller.endTick(havePixels: true) == .seconds(1))
}

@Test func unreachableUsesTheSlowRetry() {
    var poller = MirrorPoller(cadence: .seconds(1), unreachable: .seconds(3))
    poller.record(.failed)
    #expect(poller.endTick(havePixels: false) == .seconds(3))
    poller.record(.failed)
    #expect(poller.endTick(havePixels: false) == .seconds(3))
}

@Test func throttledTickBacksOffEvenWhenTheDirectReadSucceeded() {
    var poller = MirrorPoller(cadence: .seconds(1))
    poller.record(.throttled(.seconds(4)))
    #expect(poller.endTick(havePixels: true) >= .seconds(4))
}

@Test func throttledTicksEscalateAndThenRecover() {
    var poller = MirrorPoller(cadence: .seconds(1))
    poller.record(.throttled(.seconds(1)))
    let first = poller.endTick(havePixels: true)
    poller.record(.throttled(.seconds(1)))
    let second = poller.endTick(havePixels: true)
    #expect(second > first)

    poller.record(.pixels)
    #expect(poller.endTick(havePixels: true) == .seconds(1))
}

@Test func unprobedTickIsPacedByTheDirectRead() {
    var poller = MirrorPoller(cadence: .seconds(1), unreachable: .seconds(3), reprobeEvery: 100)
    poller.record(.throttled(.seconds(8)))
    _ = poller.endTick(havePixels: true)
    #expect(poller.endTick(havePixels: true) == .seconds(1))
}
