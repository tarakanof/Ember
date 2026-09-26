import Foundation

/// GET /v1/clock/health — server→clock publish record, the clock's own
/// telemetry (cached server-side for 30 s), and the latest NG release.
public struct ClockHealth: Decodable, Sendable, Equatable {
    public struct Publish: Decodable, Sendable, Equatable {
        /// Server start; every counter resets on restart.
        public var countingSince: Date
        public var ok24h: Int
        public var fail24h: Int
        /// ok / (ok + fail) over the last 24 h, 0...1; nil without publishes.
        public var successRatio24h: Double?
        public var okTotal: Int
        public var failTotal: Int
        /// Lost first attempts that a retry recovered.
        public var retriesTotal: Int
        public var lastAt: Date?
        public var lastOk: Bool

        enum CodingKeys: String, CodingKey {
            case countingSince = "counting_since"
            case ok24h = "ok_24h"
            case fail24h = "fail_24h"
            case successRatio24h = "success_ratio_24h"
            case okTotal = "ok_total"
            case failTotal = "fail_total"
            case retriesTotal = "retries_total"
            case lastAt = "last_at"
            case lastOk = "last_ok"
        }
    }

    /// Telemetry fields are nil when the clock is unreachable or its firmware
    /// doesn't report them. No IP, SSID, UID or hostname is served.
    public struct Device: Decodable, Sendable, Equatable {
        public var reachable: Bool
        public var checkedAt: Date
        public var firmware: String?
        public var currentApp: String?
        public var uptimeSec: Int?
        public var freeHeapBytes: Int?
        public var minFreeHeapBytes: Int?
        public var wifiRssiDbm: Int?
        /// Wi-Fi (re)connects since boot; above 1 means the link dropped.
        public var wifiConnects: Int?
        public var resetReason: String?
        public var fps: Double?
        public var matrixPower: Bool?
        public var batteryPercent: Double?
        public var lowBattery: Bool?
        public var temperatureC: Double?
        public var humidityPercent: Double?

        enum CodingKeys: String, CodingKey {
            case reachable, firmware, fps
            case checkedAt = "checked_at"
            case currentApp = "current_app"
            case uptimeSec = "uptime_sec"
            case freeHeapBytes = "free_heap_bytes"
            case minFreeHeapBytes = "min_free_heap_bytes"
            case wifiRssiDbm = "wifi_rssi_dbm"
            case wifiConnects = "wifi_connects"
            case resetReason = "reset_reason"
            case matrixPower = "matrix_power"
            case batteryPercent = "battery_percent"
            case lowBattery = "low_battery"
            case temperatureC = "temperature_c"
            case humidityPercent = "humidity_percent"
        }
    }

    public var generatedAt: Date
    public var publish: Publish
    /// nil when the server has no clock configured.
    public var device: Device?
    /// Newest awtrix-ng release ("1.1.2"); nil when the server couldn't look it up.
    public var latestFirmware: String?
    /// nil when either version is unknown.
    public var updateAvailable: Bool?

    enum CodingKeys: String, CodingKey {
        case publish, device
        case generatedAt = "generated_at"
        case latestFirmware = "latest_firmware"
        case updateAvailable = "update_available"
    }
}
