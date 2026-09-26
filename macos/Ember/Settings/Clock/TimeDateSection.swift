import SwiftUI
import EmberKit

/// The Time and Date apps: clock style, time and date format, the calendar
/// box and the weekday bar.
struct TimeDateSection: View {
    @Environment(DeviceSettingsModel.self) private var device

    var body: some View {
        let s = device.settings
        let style = ClockTimeStyle(rawValue: s.draft.timeMode ?? 1) ?? .calendarBarBelow
        let h24 = s.draft.time24h ?? true
        Section {
            Picker("Clock style", selection: s.binding(\.timeMode, 1)) {
                ForEach(ClockTimeStyle.allCases) { Text(title($0)).tag($0.rawValue) }
            }
            Toggle("24-hour time", isOn: s.binding(\.time24h, true))
            Toggle("Leading zero", isOn: s.binding(\.timeLeadingZero, true))
            Toggle("Show seconds", isOn: s.binding(\.timeShowSeconds, false))
                .disabled(!style.showsSecondsAndAmPm)
            Toggle("Show AM/PM", isOn: s.binding(\.timeShowAmPm, false))
                .disabled(h24 || !style.showsSecondsAndAmPm)
            Picker("Colon", selection: s.option(\.timeSeparatorMode, TimeSeparatorMode.pulse)) {
                Text("Steady").tag(TimeSeparatorMode.steady)
                Text("Blinking").tag(TimeSeparatorMode.blink)
                Text("Pulsing").tag(TimeSeparatorMode.pulse)
            }
            LabeledContent("Preview") {
                Text(verbatim: DeviceKnownValues.timePreview(
                    hour24: h24, leadingZero: s.draft.timeLeadingZero ?? true,
                    showSeconds: (s.draft.timeShowSeconds ?? false) && style.showsSecondsAndAmPm,
                    showAmPm: (s.draft.timeShowAmPm ?? false) && style.showsSecondsAndAmPm))
                .monospacedDigit()
            }
        } header: {
            Text("Time")
        } footer: {
            if !style.showsSecondsAndAmPm {
                Text("This clock style has no room for seconds or AM/PM.")
            }
        }

        Section("Date") {
            Picker("Order", selection: s.option(\.dateOrder, DateOrder.dayMonthYear)) {
                Text("Day, month, year").tag(DateOrder.dayMonthYear)
                Text("Month, day, year").tag(DateOrder.monthDayYear)
                Text("Year, month, day").tag(DateOrder.yearMonthDay)
            }
            Picker("Separator", selection: s.option(\.dateSeparator, DateSeparator.dot)) {
                ForEach(DateSeparator.allCases) { Text(verbatim: $0.symbol).tag($0) }
            }
            Picker("Year", selection: s.option(\.dateYearMode, DateYearMode.twoDigit)) {
                Text("Hidden").tag(DateYearMode.none)
                Text("2 digits").tag(DateYearMode.twoDigit)
                Text("4 digits").tag(DateYearMode.fourDigit)
            }
            Toggle("Show weekday", isOn: s.binding(\.dateShowWeekday, false))
            Toggle("Month names", isOn: s.binding(\.dateMonthNames, false))
            LabeledContent("Preview") {
                Text(verbatim: DeviceKnownValues.datePreview(
                    order: s.option(\.dateOrder, DateOrder.dayMonthYear).wrappedValue,
                    separator: s.option(\.dateSeparator, DateSeparator.dot).wrappedValue,
                    yearMode: s.option(\.dateYearMode, DateYearMode.twoDigit).wrappedValue))
                .monospacedDigit()
            }
        }

        Section {
            HexColorRow(title: "Header", hex: s.binding(\.calendarHeaderColor, "#FF0000"), fallback: "#FF0000")
            HexColorRow(title: "Background", hex: s.binding(\.calendarBodyColor, "#FFFFFF"))
            HexColorRow(title: "Day number", hex: s.binding(\.calendarTextColor, "#000000"), fallback: "#000000")
        } header: {
            Text("Calendar Box")
        } footer: {
            Text("Drawn by the calendar clock styles; the header only in the styles that have one.")
        }
        .disabled(!style.drawsCalendarBox)

        Section {
            Toggle("Show weekday bar", isOn: s.binding(\.weekdayBar, \.show, true, empty: WeekdayBar()))
            Toggle("Start on Monday", isOn: s.binding(\.weekdayBar, \.startOnMonday, true, empty: WeekdayBar()))
            HexColorRow(title: "Today", hex: s.binding(\.weekdayBar, \.activeColor, "#FFFFFF", empty: WeekdayBar()))
            HexColorRow(title: "Other days",
                        hex: s.binding(\.weekdayBar, \.inactiveColor, "#666666", empty: WeekdayBar()),
                        fallback: "#666666")
        } header: {
            Text("Weekday Bar")
        } footer: {
            if !style.drawsWeekdayBar {
                Text("This clock style draws no weekday bar; the Date app still does.")
            }
        }
    }

    private func title(_ style: ClockTimeStyle) -> LocalizedStringKey {
        switch style {
        case .centered: "Centered"
        case .calendarBarBelow: "Calendar, bar below"
        case .calendarBarAbove: "Calendar, bar above"
        case .notchedBarBelow: "Notched calendar, bar below"
        case .notchedBarAbove: "Notched calendar, bar above"
        case .bigDigits: "Big digits"
        case .binary: "Binary"
        }
    }
}
