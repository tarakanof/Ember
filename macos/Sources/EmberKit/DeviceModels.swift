import Foundation

public struct ScrollSettings: Codable, Equatable, Sendable {
    public var mode: String?
    public var direction: String?
    public var entry: String?
    public var whenFits: String?
    public var speed: Int?
    public var gap: Int?
    public var holdMs: Int?

    public init(mode: String? = nil, direction: String? = nil, entry: String? = nil, whenFits: String? = nil,
                speed: Int? = nil, gap: Int? = nil, holdMs: Int? = nil) {
        self.mode = mode; self.direction = direction; self.entry = entry; self.whenFits = whenFits
        self.speed = speed; self.gap = gap; self.holdMs = holdMs
    }
}

public struct WeekdayBar: Codable, Equatable, Sendable {
    public var show: Bool?
    public var startOnMonday: Bool?
    public var activeColor: String?
    public var inactiveColor: String?

    public init(show: Bool? = nil, startOnMonday: Bool? = nil, activeColor: String? = nil, inactiveColor: String? = nil) {
        self.show = show; self.startOnMonday = startOnMonday
        self.activeColor = activeColor; self.inactiveColor = inactiveColor
    }
}

public struct DeviceSettings: Codable, Equatable, Sendable {
    public var brightness: Int?
    public var autoBrightness: Bool?
    public var soundEnabled: Bool?
    public var buzzerVolume: Int?
    public var appDurationMs: Int?
    public var autoTransition: Bool?
    public var transitionDurationMs: Int?
    public var transitionEffect: String?
    public var textColor: String?
    public var uppercase: Bool?
    public var blockNavigation: Bool?
    public var timeMode: Int?
    public var time24h: Bool?
    public var timeLeadingZero: Bool?
    public var timeShowSeconds: Bool?
    public var timeShowAmPm: Bool?
    public var timeSeparatorMode: String?
    public var dateOrder: String?
    public var dateSeparator: String?
    public var dateYearMode: String?
    public var dateShowWeekday: Bool?
    public var dateMonthNames: Bool?
    public var calendarHeaderColor: String?
    public var calendarBodyColor: String?
    public var calendarTextColor: String?
    public var timeColor: String?
    public var dateColor: String?
    public var temperatureColor: String?
    public var humidityColor: String?
    public var batteryColor: String?
    public var useCelsius: Bool?
    public var scroll: ScrollSettings?
    public var weekdayBar: WeekdayBar?

    enum CodingKeys: String, CodingKey {
        case brightness, autoBrightness, soundEnabled, buzzerVolume, appDurationMs, autoTransition, transitionDurationMs
        case transitionEffect, textColor, uppercase, blockNavigation
        case timeMode, time24h, timeLeadingZero, timeShowSeconds, timeShowAmPm, timeSeparatorMode
        case dateOrder, dateSeparator, dateYearMode, dateShowWeekday, dateMonthNames
        case calendarHeaderColor, calendarBodyColor, calendarTextColor
        case timeColor, dateColor, temperatureColor, humidityColor, batteryColor
        case useCelsius
        case scroll, weekdayBar
    }

    public init() {}

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func i(_ k: CodingKeys) -> Int? { (try? c.decodeIfPresent(Int.self, forKey: k)) ?? nil }
        func b(_ k: CodingKeys) -> Bool? { (try? c.decodeIfPresent(Bool.self, forKey: k)) ?? nil }
        func s(_ k: CodingKeys) -> String? { (try? c.decodeIfPresent(String.self, forKey: k)) ?? nil }
        brightness = i(.brightness); autoBrightness = b(.autoBrightness)
        soundEnabled = b(.soundEnabled); buzzerVolume = i(.buzzerVolume)
        appDurationMs = i(.appDurationMs); autoTransition = b(.autoTransition)
        transitionDurationMs = i(.transitionDurationMs); transitionEffect = s(.transitionEffect)
        textColor = s(.textColor); uppercase = b(.uppercase); blockNavigation = b(.blockNavigation)
        timeMode = i(.timeMode); time24h = b(.time24h); timeLeadingZero = b(.timeLeadingZero)
        timeShowSeconds = b(.timeShowSeconds); timeShowAmPm = b(.timeShowAmPm)
        timeSeparatorMode = s(.timeSeparatorMode)
        dateOrder = s(.dateOrder); dateSeparator = s(.dateSeparator); dateYearMode = s(.dateYearMode)
        dateShowWeekday = b(.dateShowWeekday); dateMonthNames = b(.dateMonthNames)
        calendarHeaderColor = s(.calendarHeaderColor); calendarBodyColor = s(.calendarBodyColor)
        calendarTextColor = s(.calendarTextColor)
        timeColor = s(.timeColor); dateColor = s(.dateColor); temperatureColor = s(.temperatureColor)
        humidityColor = s(.humidityColor); batteryColor = s(.batteryColor)
        useCelsius = b(.useCelsius)
        scroll = (try? c.decodeIfPresent(ScrollSettings.self, forKey: .scroll)) ?? nil
        weekdayBar = (try? c.decodeIfPresent(WeekdayBar.self, forKey: .weekdayBar)) ?? nil
    }
}

public struct WifiInfo: Codable, Equatable, Sendable {
    public var ssid: String?
    public var rssi: Int?
    public var ip: String?

    public init(ssid: String? = nil, rssi: Int? = nil, ip: String? = nil) {
        self.ssid = ssid; self.rssi = rssi; self.ip = ip
    }
}

public struct DeviceStats: Codable, Equatable, Sendable {
    public var version: String?
    public var uid: String?
    public var hostname: String?
    public var batteryPercent: Double?
    public var batteryVoltage: Double?
    public var temperature: Double?
    public var humidity: Double?
    public var lightLevel: Double?
    public var brightness: Int?
    public var fps: Double?
    public var freeHeapBytes: Int?
    public var uptimeSeconds: Int?
    public var currentApp: String?
    public var wifi: WifiInfo?

    enum CodingKeys: String, CodingKey {
        case version, uid, hostname, batteryPercent, batteryVoltage, temperature, humidity
        case lightLevel, brightness, fps, freeHeapBytes, uptimeSeconds, currentApp, wifi
    }

    public init() {}

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func i(_ k: CodingKeys) -> Int? { (try? c.decodeIfPresent(Int.self, forKey: k)) ?? nil }
        func d(_ k: CodingKeys) -> Double? { (try? c.decodeIfPresent(Double.self, forKey: k)) ?? nil }
        func s(_ k: CodingKeys) -> String? { (try? c.decodeIfPresent(String.self, forKey: k)) ?? nil }
        version = s(.version); uid = s(.uid); hostname = s(.hostname)
        batteryPercent = d(.batteryPercent); batteryVoltage = d(.batteryVoltage)
        temperature = d(.temperature); humidity = d(.humidity); lightLevel = d(.lightLevel)
        brightness = i(.brightness); fps = d(.fps); freeHeapBytes = i(.freeHeapBytes)
        uptimeSeconds = i(.uptimeSeconds); currentApp = s(.currentApp)
        wifi = (try? c.decodeIfPresent(WifiInfo.self, forKey: .wifi)) ?? nil
    }
}

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

public struct DeviceDisplay: Codable, Equatable, Sendable {
    public var overlay: String?
    public var overlaySettings: OverlaySettings?
    public var power: Bool?

    enum CodingKeys: String, CodingKey { case overlay, overlaySettings, power }

    public init(overlay: String? = nil, overlaySettings: OverlaySettings? = nil, power: Bool? = nil) {
        self.overlay = overlay
        self.overlaySettings = overlaySettings
        self.power = power
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        overlay = (try? c.decodeIfPresent(String.self, forKey: .overlay)) ?? nil
        overlaySettings = (try? c.decodeIfPresent(OverlaySettings.self, forKey: .overlaySettings)) ?? nil
        power = (try? c.decodeIfPresent(Bool.self, forKey: .power)) ?? nil
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(overlay, forKey: .overlay)
        try c.encodeIfPresent(overlaySettings, forKey: .overlaySettings)
    }
}

public struct OverlaySettings: Codable, Equatable, Sendable {
    public var speed: Double?
    public var palette: String?
    public var blend: Bool?

    public init(speed: Double? = nil, palette: String? = nil, blend: Bool? = nil) {
        self.speed = speed; self.palette = palette; self.blend = blend
    }
}

public enum OverlayEffect: String, CaseIterable, Identifiable, Sendable {
    case drizzle, frost, rain, snow, storm, thunder

    public var id: String { rawValue }
    public var displayName: String { rawValue.capitalized }
}

public struct AppInfo: Codable, Equatable, Sendable, Identifiable {
    public var name: String
    public var enabled: Bool
    public var inLoop: Bool
    public var origin: String?
    public var present: Bool?
    public var slot: Int?

    public var id: String { name }

    enum CodingKeys: String, CodingKey { case name, enabled, inLoop, origin, present, slot }

    public init(name: String, enabled: Bool = true, inLoop: Bool = true, origin: String? = nil,
                present: Bool? = nil, slot: Int? = nil) {
        self.name = name; self.enabled = enabled; self.inLoop = inLoop; self.origin = origin
        self.present = present; self.slot = slot
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        enabled = (try? c.decodeIfPresent(Bool.self, forKey: .enabled)) ?? true
        inLoop = (try? c.decodeIfPresent(Bool.self, forKey: .inLoop)) ?? true
        origin = (try? c.decodeIfPresent(String.self, forKey: .origin)) ?? nil
        present = (try? c.decodeIfPresent(Bool.self, forKey: .present)) ?? nil
        slot = (try? c.decodeIfPresent(Int.self, forKey: .slot)) ?? nil
    }
}

public struct AppsUpdate: Codable, Equatable, Sendable {
    public var order: [String]
    public var disabled: [String]

    public init(order: [String] = [], disabled: [String] = []) {
        self.order = order
        self.disabled = disabled
    }
}

public struct DeviceAudioCapabilities: Codable, Equatable, Sendable {
    public var buzzer: Bool
    public var track: Bool
    public var mp3: Bool
    public var radio: Bool

    enum CodingKeys: String, CodingKey { case buzzer, track, mp3, radio }

    public init(buzzer: Bool = false, track: Bool = false, mp3: Bool = false, radio: Bool = false) {
        self.buzzer = buzzer; self.track = track; self.mp3 = mp3; self.radio = radio
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func b(_ k: CodingKeys) -> Bool { (try? c.decodeIfPresent(Bool.self, forKey: k)) ?? false }
        buzzer = b(.buzzer); track = b(.track); mp3 = b(.mp3); radio = b(.radio)
    }
}

public struct DeviceCapabilities: Codable, Equatable, Sendable {
    public var effects: [String]
    public var paletteEffects: [String]
    public var transitions: [String]
    public var overlays: [String]
    public var palettes: [String]
    public var audio: DeviceAudioCapabilities?

    enum CodingKeys: String, CodingKey { case effects, paletteEffects, transitions, overlays, palettes, audio }

    public init(effects: [String] = [], paletteEffects: [String] = [], transitions: [String] = [],
                overlays: [String] = [], palettes: [String] = [], audio: DeviceAudioCapabilities? = nil) {
        self.effects = effects; self.paletteEffects = paletteEffects; self.transitions = transitions
        self.overlays = overlays; self.palettes = palettes; self.audio = audio
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func arr(_ k: CodingKeys) -> [String] { (try? c.decodeIfPresent([String].self, forKey: k)) ?? [] }
        effects = arr(.effects); paletteEffects = arr(.paletteEffects); transitions = arr(.transitions)
        overlays = arr(.overlays); palettes = arr(.palettes)
        audio = (try? c.decodeIfPresent(DeviceAudioCapabilities.self, forKey: .audio)) ?? nil
    }

    public var hasBuzzer: Bool { audio?.buzzer ?? true }
}

public struct ScreenFrame: Codable, Equatable, Sendable {
    public var width: Int
    public var height: Int
    public var pixels: [Int]

    public init(width: Int = 32, height: Int = 8, pixels: [Int] = []) {
        self.width = width; self.height = height; self.pixels = pixels
    }
}

public struct DeviceConfig: Codable, Equatable, Sendable {
    public var baseURL: String
    public var source: String
    enum CodingKeys: String, CodingKey { case baseURL = "base_url", source }
    public init(baseURL: String = "", source: String = "none") {
        self.baseURL = baseURL; self.source = source
    }

    public var webURL: URL? {
        let trimmed = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        guard let url = URL(string: trimmed.hasSuffix("/") ? String(trimmed.dropLast()) : trimmed),
              let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let host = url.host, !host.isEmpty
        else { return nil }
        return url
    }
}

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

public struct DiscoverResult: Codable, Equatable, Sendable {
    public var candidates: [DiscoveredClock]
    public var effective: String
    public var source: String
}

public struct ButtonStatus: Codable, Equatable, Sendable {
    public var expectedCallback: String?
    public var configuredCallback: String?
    public var configured: Bool?
    public var lastPressUnix: Int?
    public var secondsSince: Int?
    enum CodingKeys: String, CodingKey {
        case expectedCallback = "expected_callback"
        case configuredCallback = "configured_callback"
        case configured
        case lastPressUnix = "last_press_unix"
        case secondsSince = "seconds_since"
    }
    public init() {}
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        expectedCallback = (try? c.decodeIfPresent(String.self, forKey: .expectedCallback)) ?? nil
        configuredCallback = (try? c.decodeIfPresent(String.self, forKey: .configuredCallback)) ?? nil
        configured = (try? c.decodeIfPresent(Bool.self, forKey: .configured)) ?? nil
        lastPressUnix = (try? c.decodeIfPresent(Int.self, forKey: .lastPressUnix)) ?? nil
        secondsSince = (try? c.decodeIfPresent(Int.self, forKey: .secondsSince)) ?? nil
    }
}

public struct ButtonsUpdate: Codable, Equatable, Sendable {
    public var enabled: Bool
    public init(enabled: Bool) { self.enabled = enabled }
}

public struct DisplayPowerUpdate: Codable, Equatable, Sendable {
    public var power: Bool
    public init(power: Bool) { self.power = power }
}

public struct AudioTestRequest: Codable, Equatable, Sendable {
    public var melody: String
    public init(melody: String) { self.melody = melody }
}

public struct DeviceMelody: Codable, Equatable, Sendable, Identifiable {
    public var name: String
    public var rtttl: String
    public var bytes: Int
    public var notes: Int
    public var durationMs: Int
    public var valid: Bool
    public var error: String?
    public var index: Int?

    public var id: String { name }

    public init(name: String, rtttl: String = "", bytes: Int = 0, notes: Int = 0,
                durationMs: Int = 0, valid: Bool = true, error: String? = nil, index: Int? = nil) {
        self.name = name; self.rtttl = rtttl; self.bytes = bytes; self.notes = notes
        self.durationMs = durationMs; self.valid = valid; self.error = error; self.index = index
    }
}

public struct DeviceMelodyList: Codable, Equatable, Sendable {
    public var melodies: [DeviceMelody]
    public var usedBytes: Int
    public var totalBytes: Int

    public init(melodies: [DeviceMelody] = [], usedBytes: Int = 0, totalBytes: Int = 0) {
        self.melodies = melodies; self.usedBytes = usedBytes; self.totalBytes = totalBytes
    }
}
