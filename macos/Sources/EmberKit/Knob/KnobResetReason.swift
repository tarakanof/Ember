import Foundation

public enum KnobResetReason {
    public static let known = [
        "unknown", "poweron", "ext", "sw", "panic", "int_wdt", "task_wdt", "wdt", "deepsleep",
        "brownout", "sdio", "usb", "jtag", "efuse", "pwr_glitch", "cpu_lockup", "lvgl_stall",
    ]

    public static func label(_ raw: String) -> String {
        switch raw {
        case "unknown": String(localized: "Unknown")
        case "poweron": String(localized: "Power on")
        case "sw": String(localized: "Restarted by software")
        case "panic": String(localized: "Crash")
        case "int_wdt", "task_wdt", "wdt": String(localized: "Watchdog")
        case "brownout": String(localized: "Low voltage")
        case "deepsleep": String(localized: "Woke from sleep")
        case "ext": String(localized: "Reset pin")
        case "sdio": String(localized: "SDIO reset")
        case "usb": String(localized: "USB reset (flashing or serial)")
        case "jtag": String(localized: "Debugger reset")
        case "efuse": String(localized: "eFuse error")
        case "pwr_glitch": String(localized: "Power glitch")
        case "cpu_lockup": String(localized: "CPU lockup")
        case "lvgl_stall": String(localized: "Display stalled")
        default: raw
        }
    }
}
