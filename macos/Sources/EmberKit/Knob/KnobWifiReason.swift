import Foundation

public enum KnobWifiReason {
    public static func label(_ code: Int) -> String? {
        switch code {
        case 1: String(localized: "unspecified", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 2: String(localized: "authentication expired", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 3: String(localized: "access point left", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 4: String(localized: "dropped for inactivity", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 5: String(localized: "access point full", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 6: String(localized: "frame from an unauthenticated station", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 7: String(localized: "frame from an unassociated station", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 8: String(localized: "disconnected itself", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 14: String(localized: "message integrity failure", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 15: String(localized: "4-way handshake timeout", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 16: String(localized: "group key update timeout", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 23: String(localized: "802.1X authentication failed", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 34: String(localized: "missing acknowledgements", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 39: String(localized: "timeout", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 200: String(localized: "beacon timeout", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 201: String(localized: "no access point found", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 202: String(localized: "authentication failed", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 203: String(localized: "association failed", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 204: String(localized: "handshake timeout", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 205: String(localized: "connection failed", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 206: String(localized: "access point clock reset", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 207: String(localized: "roaming", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 208: String(localized: "association comeback time too long", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 209: String(localized: "SA query timeout", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 210: String(localized: "no access point with compatible security", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 211: String(localized: "no access point in the security threshold", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        case 212: String(localized: "no access point above the signal threshold", comment: "Settings › Knob Reconnects row: an ESP-IDF Wi-Fi disconnect reason, after its code (\"3 (last: 203 association failed)\").")
        default: nil
        }
    }
}
