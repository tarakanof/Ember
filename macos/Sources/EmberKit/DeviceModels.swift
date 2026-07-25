import Foundation

/// Mirror of the AWTRIX /api/settings keys Ember exposes through
/// /v1/device/settings. Every field is optional so a partial PUT only encodes
/// the values that are set, and a single unexpected type from the device (e.g. a
/// colour returned as an array instead of a hex string) leaves just that field
/// nil instead of failing the whole load.
public struct DeviceSettings: Codable, Equatable, Sendable {
    // General
    public var bri: Int?
    public var abri: Bool?
    public var vol: Int?
    public var atime: Int?
    public var atrans: Bool?
    public var teff: Int?
    public var tspeed: Int?
    public var sspeed: Int?
    public var tcol: String?
    public var uppercase: Bool?
    public var blockn: Bool?
    public var overlay: String?
    // Native apps
    public var tim: Bool?
    public var dat: Bool?
    public var temp: Bool?
    public var hum: Bool?
    public var bat: Bool?
    // Time & Date
    public var tformat: String?
    public var dformat: String?
    public var som: Bool?
    public var tmode: Int?
    public var chcol: String?
    public var cbcol: String?
    public var ctcol: String?
    public var wd: Bool?
    public var wdca: String?
    public var wdci: String?

    enum CodingKeys: String, CodingKey {
        case bri = "BRI", abri = "ABRI", vol = "VOL", atime = "ATIME", atrans = "ATRANS"
        case teff = "TEFF", tspeed = "TSPEED", sspeed = "SSPEED", tcol = "TCOL"
        case uppercase = "UPPERCASE", blockn = "BLOCKN", overlay = "OVERLAY"
        case tim = "TIM", dat = "DAT", temp = "TEMP", hum = "HUM", bat = "BAT"
        case tformat = "TFORMAT", dformat = "DFORMAT", som = "SOM", tmode = "TMODE"
        case chcol = "CHCOL", cbcol = "CBCOL", ctcol = "CTCOL", wd = "WD", wdca = "WDCA", wdci = "WDCI"
    }

    public init() {}

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func i(_ k: CodingKeys) -> Int? { (try? c.decodeIfPresent(Int.self, forKey: k)) ?? nil }
        func b(_ k: CodingKeys) -> Bool? { (try? c.decodeIfPresent(Bool.self, forKey: k)) ?? nil }
        func s(_ k: CodingKeys) -> String? { (try? c.decodeIfPresent(String.self, forKey: k)) ?? nil }
        bri = i(.bri); abri = b(.abri); vol = i(.vol); atime = i(.atime); atrans = b(.atrans)
        teff = i(.teff); tspeed = i(.tspeed); sspeed = i(.sspeed); tcol = s(.tcol)
        uppercase = b(.uppercase); blockn = b(.blockn); overlay = s(.overlay)
        tim = b(.tim); dat = b(.dat); temp = b(.temp); hum = b(.hum); bat = b(.bat)
        tformat = s(.tformat); dformat = s(.dformat); som = b(.som); tmode = i(.tmode)
        chcol = s(.chcol); cbcol = s(.cbcol); ctcol = s(.ctcol); wd = b(.wd); wdca = s(.wdca); wdci = s(.wdci)
    }
    // The compiler-synthesised encode(to:) uses encodeIfPresent for each optional,
    // so nil fields are omitted — exactly what a partial settings PUT wants.
}

/// Subset of AWTRIX /api/stats shown in the Device tab header.
public struct DeviceStats: Codable, Equatable, Sendable {
    public var bat: Int?
    public var version: String?
    public var ram: Int?
    /// The matrix's current effective brightness (0–255) — reflects
    /// auto-brightness dimming, unlike the BRI setting.
    public var bri: Int?
    /// Internal sensor readings (already offset-corrected by the firmware).
    /// Absent when the clock's sensor_reading is disabled.
    public var temp: Double?
    public var hum: Double?
    public var lux: Double?

    enum CodingKeys: String, CodingKey { case bat, version, ram, bri, temp, hum, lux }

    public init() {}
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        bat = (try? c.decodeIfPresent(Int.self, forKey: .bat)) ?? nil
        version = (try? c.decodeIfPresent(String.self, forKey: .version)) ?? nil
        ram = (try? c.decodeIfPresent(Int.self, forKey: .ram)) ?? nil
        bri = (try? c.decodeIfPresent(Int.self, forKey: .bri)) ?? nil
        temp = (try? c.decodeIfPresent(Double.self, forKey: .temp)) ?? nil
        hum = (try? c.decodeIfPresent(Double.self, forKey: .hum)) ?? nil
        lux = (try? c.decodeIfPresent(Double.self, forKey: .lux)) ?? nil
    }
}

/// The clock's sensor calibration offsets (GET/PUT /v1/device/sensors), stored
/// in dev.json on the device. nil = not set, the firmware default applies.
/// encode(to:) writes explicit nulls — the server treats null as "remove the
/// key" (reset to firmware default), while an absent key means "leave as is".
public struct SensorCalibration: Codable, Equatable, Sendable {
    public var tempOffset: Double?
    public var humOffset: Double?

    enum CodingKeys: String, CodingKey {
        case tempOffset = "temp_offset"
        case humOffset = "hum_offset"
    }

    public init(tempOffset: Double? = nil, humOffset: Double? = nil) {
        self.tempOffset = tempOffset
        self.humOffset = humOffset
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(tempOffset, forKey: .tempOffset)
        try c.encode(humOffset, forKey: .humOffset)
    }
}

/// The effective clock URL and where it came from (store/config/discovered/none).
public struct DeviceConfig: Codable, Equatable, Sendable {
    public var baseURL: String
    public var source: String
    enum CodingKeys: String, CodingKey { case baseURL = "base_url", source }
    public init(baseURL: String = "", source: String = "none") {
        self.baseURL = baseURL; self.source = source
    }
}

/// One AWTRIX device found by mDNS discovery — server-side (`/v1/device/discover`)
/// or, when the server's networking can't see multicast, by `ClockDiscovery` on
/// this Mac. Both produce the same shape so the UI needn't care which ran.
public struct DiscoveredClock: Codable, Equatable, Sendable, Identifiable {
    public var host: String
    public var baseURL: String
    public var uid: String
    public var version: String
    public var id: String { baseURL }
    enum CodingKeys: String, CodingKey { case host, baseURL = "base_url", uid, version }
    public init(host: String, baseURL: String, uid: String, version: String) {
        self.host = host; self.baseURL = baseURL; self.uid = uid; self.version = version
    }
}

/// Result of GET /v1/device/discover.
public struct DiscoverResult: Codable, Equatable, Sendable {
    public var candidates: [DiscoveredClock]
    public var effective: String
    public var source: String
}

/// GET /v1/device/buttons — the button_callback the clock should hold, and how
/// long ago the server last received a button press (nil = never).
public struct ButtonStatus: Codable, Equatable, Sendable {
    public var expectedCallback: String?
    public var lastPressUnix: Int?
    public var secondsSince: Int?
    enum CodingKeys: String, CodingKey {
        case expectedCallback = "expected_callback"
        case lastPressUnix = "last_press_unix"
        case secondsSince = "seconds_since"
    }
    public init() {}
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        expectedCallback = (try? c.decodeIfPresent(String.self, forKey: .expectedCallback)) ?? nil
        lastPressUnix = (try? c.decodeIfPresent(Int.self, forKey: .lastPressUnix)) ?? nil
        secondsSince = (try? c.decodeIfPresent(Int.self, forKey: .secondsSince)) ?? nil
    }
}
