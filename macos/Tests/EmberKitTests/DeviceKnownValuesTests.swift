import Testing
import Foundation
@testable import EmberKit

private let utc = TimeZone(identifier: "UTC")!

// 2026-06-07 14:05:09 UTC — fixed instant so format previews are deterministic.
private let referenceDate: Date = {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = utc
    return c.date(from: DateComponents(year: 2026, month: 6, day: 7, hour: 14, minute: 5, second: 9))!
}()

@Test func transitionEffectsCoverDocumentedRange() {
    // AWTRIX docs: TEFF 0–10, Random through Fade.
    #expect(TransitionEffect.allCases.count == 11)
    #expect(TransitionEffect.allCases.map(\.rawValue) == Array(0...10))
    #expect(TransitionEffect(rawValue: 0)?.displayName == "Random")
    #expect(TransitionEffect(rawValue: 1)?.displayName == "Slide")
    #expect(TransitionEffect(rawValue: 5)?.displayName == "Pixelate")
    #expect(TransitionEffect(rawValue: 6)?.displayName == "Curtain")
    #expect(TransitionEffect(rawValue: 10)?.displayName == "Fade")
}

@Test func timeFormatExamplesRender() {
    func ex(_ f: String) -> String { DeviceFormats.example(f, at: referenceDate, timeZone: utc) }
    #expect(ex("%H:%M:%S") == "14:05:09")
    #expect(ex("%H:%M") == "14:05")
    #expect(ex("%H %M") == "14 05")
    #expect(ex("%l:%M") == "2:05")
    #expect(ex("%l:%M %p") == "2:05 PM")
}

@Test func dateFormatExamplesRender() {
    func ex(_ f: String) -> String { DeviceFormats.example(f, at: referenceDate, timeZone: utc) }
    #expect(ex("%d.%m.%y") == "07.06.26")
    #expect(ex("%y-%m-%d") == "26-06-07")
    #expect(ex("%m/%d") == "06/07")
}

@Test func twelveHourTokenWrapsAtNoonAndMidnight() {
    let calendar: (Int) -> Date = { hour in
        var c = Calendar(identifier: .gregorian)
        c.timeZone = utc
        return c.date(from: DateComponents(year: 2026, month: 6, day: 7, hour: hour, minute: 0))!
    }
    #expect(DeviceFormats.example("%l %p", at: calendar(0), timeZone: utc) == "12 AM")
    #expect(DeviceFormats.example("%l %p", at: calendar(12), timeZone: utc) == "12 PM")
    #expect(DeviceFormats.example("%l %p", at: calendar(13), timeZone: utc) == "1 PM")
}

@Test func knownFormatListsAreNonEmptyAndRenderable() {
    #expect(!DeviceFormats.timeFormats.isEmpty)
    #expect(!DeviceFormats.dateFormats.isEmpty)
    for f in DeviceFormats.timeFormats + DeviceFormats.dateFormats {
        let rendered = DeviceFormats.example(f, at: referenceDate, timeZone: utc)
        #expect(!rendered.contains("%"), "unhandled token in \(f): \(rendered)")
    }
}
