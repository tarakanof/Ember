import Testing
import Foundation
@testable import EmberKit

// Being throttled says nothing about whether the proxy route EXISTS — that is
// what preferProxy tracks (an older server without /v1/device/screen 404s).
// Conflating the two made a throttled mirror stop using the proxy for 30 ticks.
@Test func throttleDoesNotDemoteTheProxy() {
    var poller = MirrorPoller()
    #expect(poller.probesProxy)
    poller.record(.throttled(.seconds(2)))
    _ = poller.endTick(havePixels: false)
    #expect(poller.probesProxy)
}

// A proxy that failed for any other reason is assumed missing, and re-probed
// only every reprobeEvery ticks.
@Test func proxyFailureDemotesUntilTheNextReprobe() {
    var poller = MirrorPoller(reprobeEvery: 4)
    poller.record(.failed)
    _ = poller.endTick(havePixels: true)
    #expect(!poller.probesProxy)          // tick 1
    _ = poller.endTick(havePixels: true)
    #expect(!poller.probesProxy)          // tick 2
    _ = poller.endTick(havePixels: true)
    #expect(!poller.probesProxy)          // tick 3
    _ = poller.endTick(havePixels: true)
    #expect(poller.probesProxy)           // tick 4: re-probe
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

// The direct read goes to the clock's own address and spends no server budget,
// so a throttle is the moment it is MOST worth doing. Skipping it here is what
// painted the mirror black for the whole throttled window.
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

// Nothing reachable at all: slow down, but this is not the limiter's doing, so
// it must not escalate like one.
@Test func unreachableUsesTheSlowRetry() {
    var poller = MirrorPoller(cadence: .seconds(1), unreachable: .seconds(3))
    poller.record(.failed)
    #expect(poller.endTick(havePixels: false) == .seconds(3))
    poller.record(.failed)
    #expect(poller.endTick(havePixels: false) == .seconds(3))
}

// A throttled tick waits at least what the server asked, even when the direct
// read saved the frame — the proxy is still throttled either way.
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

// A tick that never probed the proxy (demoted, not a re-probe tick) is paced by
// whatever the direct read managed, not by a stale earlier outcome.
@Test func unprobedTickIsPacedByTheDirectRead() {
    var poller = MirrorPoller(cadence: .seconds(1), unreachable: .seconds(3), reprobeEvery: 100)
    poller.record(.throttled(.seconds(8)))
    _ = poller.endTick(havePixels: true)
    // No record() this tick: the proxy was not asked, so the previous tick's
    // throttle must not still be pacing this one.
    #expect(poller.endTick(havePixels: true) == .seconds(1))
}
