import Foundation

public enum KnobCrashReason {
    public static let known = ["panic", "int_wdt", "task_wdt", "wdt", "lvgl_stall", "unknown"]

    public static func label(_ raw: String) -> String {
        switch raw {
        case "panic": String(localized: "Panic")
        case "int_wdt": String(localized: "Interrupt watchdog")
        case "task_wdt": String(localized: "Task watchdog")
        case "wdt": String(localized: "Watchdog")
        case "lvgl_stall": String(localized: "Display stalled")
        case "unknown": String(localized: "Unknown")
        default: raw
        }
    }
}
