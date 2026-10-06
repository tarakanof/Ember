import Foundation

public struct ClockHealth: Decodable, Sendable, Equatable {
    public struct Publish: Decodable, Sendable, Equatable {
        public var countingSince: Date
        public var ok24h: Int
        public var fail24h: Int
        public var successRatio24h: Double?
        public var okTotal: Int
        public var failTotal: Int
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

    public struct Device: Decodable, Sendable, Equatable {
        public var reachable: Bool
        public var checkedAt: Date
        public var firmware: String?
        public var currentApp: String?
        public var uptimeSec: Int?
        public var freeHeapBytes: Int?
        public var minFreeHeapBytes: Int?
        public var wifiRssiDbm: Int?
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
    public var device: Device?
    public var latestFirmware: String?
    public var updateAvailable: Bool?

    enum CodingKeys: String, CodingKey {
        case publish, device
        case generatedAt = "generated_at"
        case latestFirmware = "latest_firmware"
        case updateAvailable = "update_available"
    }
}
