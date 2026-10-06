import Foundation

public enum DeviceUnits {
    public static func brightnessPercent(raw: Int) -> Int {
        Int((Double(min(max(raw, 0), 255)) * 100 / 255).rounded())
    }

    public static func brightnessRaw(percent: Int) -> Int {
        Int((Double(min(max(percent, 0), 100)) * 255 / 100).rounded())
    }

    public static let temperatureOffsetRange: ClosedRange<Double> = -20...20
    public static let humidityOffsetRange: ClosedRange<Double> = -50...50
    public static let firmwareTemperatureOffset = -9.0
    public static let firmwareHumidityOffset = 0.0
}

public enum ClockTimeStyle: Int, CaseIterable, Identifiable, Sendable {
    case centered = 0
    case calendarBarBelow = 1
    case calendarBarAbove = 2
    case notchedBarBelow = 3
    case notchedBarAbove = 4
    case bigDigits = 5
    case binary = 6

    public var id: Int { rawValue }

    public var showsSecondsAndAmPm: Bool { self == .centered }
    public var drawsCalendarBox: Bool { (1...4).contains(rawValue) }
    public var drawsWeekdayBar: Bool { rawValue <= 4 }
}

public enum ScrollMode: String, CaseIterable, Identifiable, Sendable {
    case `static`, wrap, loop, bounce
    public var id: String { rawValue }
}

public enum NativeAppsPlan {
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

    private static func orderOfEnabled(_ apps: [AppInfo]) -> [String] {
        apps.filter { a in
            a.origin != "module" && a.enabled && (a.present != false || a.slot != nil)
        }.map(\.name)
    }
}
