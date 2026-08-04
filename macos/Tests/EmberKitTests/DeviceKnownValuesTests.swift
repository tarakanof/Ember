import Testing
import Foundation
@testable import EmberKit

private let utc = TimeZone(identifier: "UTC")!

// 2026-06-07 14:05:09 UTC — fixed instant so preview rendering is deterministic.
private let referenceDate: Date = {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = utc
    return c.date(from: DateComponents(year: 2026, month: 6, day: 7, hour: 14, minute: 5, second: 9))!
}()

@Test func fallbackTransitionsIsNonEmptyAndLowercase() {
    #expect(!DeviceKnownValues.fallbackTransitions.isEmpty)
    for t in DeviceKnownValues.fallbackTransitions {
        #expect(t == t.lowercased(), "\(t) should be a lowercase device-style name")
    }
}

@Test func transitionDisplayNameTitleCasesTheRawName() {
    #expect(DeviceKnownValues.displayName("random") == "Random")
    #expect(DeviceKnownValues.displayName("pixelate") == "Pixelate")
    #expect(DeviceKnownValues.displayName("") == "")
}

@Test func timePreviewRenders24HourWithSeconds() {
    let s = DeviceKnownValues.timePreview(hour24: true, leadingZero: true, showSeconds: true, showAmPm: false,
                                           at: referenceDate, timeZone: utc)
    #expect(s == "14:05:09")
}

@Test func timePreviewRenders12HourWithAmPmNoSeconds() {
    let s = DeviceKnownValues.timePreview(hour24: false, leadingZero: false, showSeconds: false, showAmPm: true,
                                           at: referenceDate, timeZone: utc)
    #expect(s == "2:05 PM")
}

@Test func timePreviewLeadingZeroPadsSingleDigitHour() {
    let s = DeviceKnownValues.timePreview(hour24: false, leadingZero: true, showSeconds: false, showAmPm: false,
                                           at: referenceDate, timeZone: utc)
    #expect(s == "02:05")
}

@Test func timePreviewTwelveHourWrapsAtNoonAndMidnight() {
    let calendar: (Int) -> Date = { hour in
        var c = Calendar(identifier: .gregorian)
        c.timeZone = utc
        return c.date(from: DateComponents(year: 2026, month: 6, day: 7, hour: hour, minute: 0))!
    }
    func preview(_ hour: Int) -> String {
        DeviceKnownValues.timePreview(hour24: false, leadingZero: false, showSeconds: false, showAmPm: true,
                                       at: calendar(hour), timeZone: utc)
    }
    #expect(preview(0) == "12:00 AM")
    #expect(preview(12) == "12:00 PM")
    #expect(preview(13) == "1:00 PM")
}

@Test func datePreviewRendersDayMonthYearWithDotAndTwoDigitYear() {
    let s = DeviceKnownValues.datePreview(order: .dayMonthYear, separator: .dot, yearMode: .twoDigit,
                                           at: referenceDate, timeZone: utc)
    #expect(s == "07.06.26")
}

@Test func datePreviewRendersYearMonthDayWithDashAndFourDigitYear() {
    let s = DeviceKnownValues.datePreview(order: .yearMonthDay, separator: .dash, yearMode: .fourDigit,
                                           at: referenceDate, timeZone: utc)
    #expect(s == "2026-06-07")
}

@Test func datePreviewHidesYearWhenYearModeIsNone() {
    let s = DeviceKnownValues.datePreview(order: .monthDayYear, separator: .slash, yearMode: .none,
                                           at: referenceDate, timeZone: utc)
    #expect(s == "06/07")
}

@Test func timeSeparatorModeCasesHaveDisplayNames() {
    #expect(TimeSeparatorMode.allCases.count == 3)
    #expect(TimeSeparatorMode.pulse.displayName == "Pulse")
}

@Test func dateOrderCasesHaveDisplayNames() {
    #expect(DateOrder.allCases.count == 3)
    #expect(DateOrder.dayMonthYear.displayName.contains("Day"))
}

@Test func dateSeparatorSymbolsMatchTheirRawMeaning() {
    #expect(DateSeparator.dot.symbol == ".")
    #expect(DateSeparator.slash.symbol == "/")
    #expect(DateSeparator.dash.symbol == "-")
}

@Test func dateYearModeCasesHaveDisplayNames() {
    #expect(DateYearMode.none.displayName == "Hidden")
    #expect(DateYearMode.twoDigit.displayName == "2-digit")
    #expect(DateYearMode.fourDigit.displayName == "4-digit")
}

@Test func overlayEffectCasesMatchTheServerEnum() {
    // Per the #67 mapping: drizzle, frost, rain, snow, storm, thunder — no "clear".
    #expect(OverlayEffect.allCases.count == 6)
    #expect(OverlayEffect(rawValue: "drizzle") != nil)
    #expect(OverlayEffect(rawValue: "clear") == nil)
}
