import Foundation

/// The ESP-IDF `esp_reset_reason_t` names the knob reports as `reset_reason`
/// (lower-cased, without the `ESP_RST_` prefix), in words.
public enum KnobResetReason {
    /// Every name ESP-IDF 5.5 defines.
    public static let known = [
        "unknown", "poweron", "ext", "sw", "panic", "int_wdt", "task_wdt", "wdt", "deepsleep",
        "brownout", "sdio", "usb", "jtag", "efuse", "pwr_glitch", "cpu_lockup",
    ]

    /// A name this app has no words for comes back as reported.
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
        default: raw
        }
    }
}
