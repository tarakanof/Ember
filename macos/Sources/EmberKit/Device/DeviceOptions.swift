import Foundation

/// Conversions between the clock's wire units and what Settings shows.
public enum DeviceUnits {
    /// NG brightness is a raw 0–255 level; Settings shows 0–100 %.
    public static func brightnessPercent(raw: Int) -> Int {
        Int((Double(min(max(raw, 0), 255)) * 100 / 255).rounded())
    }

    public static func brightnessRaw(percent: Int) -> Int {
        Int((Double(min(max(percent, 0), 100)) * 255 / 100).rounded())
    }

    /// NG's `tempOffset` limit (a larger value is a 422).
    public static let temperatureOffsetRange: ClosedRange<Double> = -20...20
    /// NG's `humOffset` limit.
    public static let humidityOffsetRange: ClosedRange<Double> = -50...50
    /// The Ulanzi build's compiled-in offsets, used when the clock reports
    /// none (−9 °C compensates the case's self-heating).
    public static let firmwareTemperatureOffset = -9.0
    public static let firmwareHumidityOffset = 0.0
}

/// NG `timeMode` (0–6). The names follow NG's settings reference.
public enum ClockTimeStyle: Int, CaseIterable, Identifiable, Sendable {
    /// Centered time, full-width weekday bar.
    case centered = 0
    /// Calendar box with a header, weekday bar at the bottom.
    case calendarBarBelow = 1
    /// Calendar box with a header, weekday bar on top.
    case calendarBarAbove = 2
    /// Calendar box with notched corners, weekday bar at the bottom.
    case notchedBarBelow = 3
    /// Calendar box with notched corners, weekday bar on top.
    case notchedBarAbove = 4
    /// Large digits punched out of a coloured field.
    case bigDigits = 5
    /// Six bits each for hours, minutes and seconds.
    case binary = 6

    public var id: Int { rawValue }

    /// Styles 1–4 reserve nine columns for the calendar box and force seconds
    /// and AM/PM off; 5 and 6 ignore both.
    public var showsSecondsAndAmPm: Bool { self == .centered }
    /// Only styles 1–4 draw the calendar box.
    public var drawsCalendarBox: Bool { (1...4).contains(rawValue) }
    /// Styles 5 and 6 draw no weekday bar.
    public var drawsWeekdayBar: Bool { rawValue <= 4 }
}

/// NG `scroll.mode`.
public enum ScrollMode: String, CaseIterable, Identifiable, Sendable {
    case `static`, wrap, loop, bounce
    public var id: String { rawValue }
}

/// Builds the `PUT /v1/device/apps` bodies for the Native Apps list. NG's
/// `disabled` only switches apps off and an app named in neither list keeps
/// what it had, so turning one on means naming it in `order`. Every body
/// orders the apps that are on (keeping pushed apps where they are, so
/// Ember's own tiles don't move, even while one is away between pushes),
/// never names a module, and never names an app in both lists.
public enum NativeAppsPlan {
    /// The apps Settings lists: the clock's built-ins. Pushed apps (Ember's
    /// own tiles) and placeholders for absent apps are left out.
    public static func listed(_ apps: [AppInfo]) -> [AppInfo] {
        apps.filter { $0.origin == "builtin" && $0.present != false }
    }

    public static func toggle(_ name: String, enabled: Bool, in apps: [AppInfo]) -> AppsUpdate {
        let next = apps.map { a in
            var a = a
            if a.name == name { a.enabled = enabled }
            return a
        }
        return AppsUpdate(order: orderOfEnabled(next), disabled: enabled ? [] : [name])
    }

    /// Moves listed apps (`source` offsets within `listed(apps)`) and keeps
    /// everything else in its place.
    public static func move(in apps: [AppInfo], fromOffsets source: IndexSet, toOffset destination: Int) -> (apps: [AppInfo], update: AppsUpdate) {
        var visible = listed(apps)
        let slots = apps.indices.filter { i in visible.contains { $0.name == apps[i].name } }
        let moving = source.map { visible[$0] }
        let insertAt = destination - source.filter { $0 < destination }.count
        for i in source.reversed() { visible.remove(at: i) }
        visible.insert(contentsOf: moving, at: min(max(insertAt, 0), visible.count))
        var next = apps
        for (slot, app) in zip(slots, visible) { next[slot] = app }
        return (next, AppsUpdate(order: orderOfEnabled(next), disabled: []))
    }

    /// What runs, in order. Modules aren't apps (a name in `order` would
    /// hold a phantom slot for them). An app that's away but has a slot (an
    /// Ember tile between pushes) keeps it.
    private static func orderOfEnabled(_ apps: [AppInfo]) -> [String] {
        apps.filter { a in
            a.origin != "module" && a.enabled && (a.present != false || a.slot != nil)
        }.map(\.name)
    }
}
