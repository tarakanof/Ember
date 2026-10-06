import Foundation

public struct KnobCheckin: Codable, Equatable, Sendable {
    public var seenAt: Date
    public var fw: String
    public var ip: String
    public var rssi: Int
    public var heapInternalFree: Int
    public var heapInternalLargest: Int
    public var uptimeS: Int
    public var appliedVersion: Int
    public var linkMHz: Int?
    public var linkFallback: Bool?
    public var wifi: KnobWifi?

    enum CodingKeys: String, CodingKey {
        case fw, ip, rssi, wifi
        case linkMHz = "link_mhz"
        case linkFallback = "link_fallback"
        case seenAt = "seen_at"
        case heapInternalFree = "heap_internal_free"
        case heapInternalLargest = "heap_internal_largest"
        case uptimeS = "uptime_s"
        case appliedVersion = "applied_version"
    }

    public init(seenAt: Date, fw: String, ip: String, rssi: Int, heapInternalFree: Int,
                heapInternalLargest: Int, uptimeS: Int, appliedVersion: Int) {
        self.seenAt = seenAt; self.fw = fw; self.ip = ip; self.rssi = rssi
        self.heapInternalFree = heapInternalFree; self.heapInternalLargest = heapInternalLargest
        self.uptimeS = uptimeS; self.appliedVersion = appliedVersion
    }
}

public struct KnobWifi: Codable, Equatable, Sendable {
    public var bssid: String?
    public var channel: Int?
    public var disconnects: Int
    public var lastReason: Int?
    public var rssiMin: Int?

    enum CodingKeys: String, CodingKey {
        case bssid, channel, disconnects
        case lastReason = "last_reason"
        case rssiMin = "rssi_min"
    }

    public var hasLinkDetails: Bool {
        bssid?.isEmpty == false || (channel ?? 0) > 0 || (rssiMin ?? 0) != 0
    }

    public init(bssid: String? = nil, channel: Int? = nil, disconnects: Int,
                lastReason: Int? = nil, rssiMin: Int? = nil) {
        self.bssid = bssid; self.channel = channel; self.disconnects = disconnects
        self.lastReason = lastReason; self.rssiMin = rssiMin
    }
}

public struct KnobDevice: Codable, Equatable, Sendable, Identifiable {
    public var id: String
    public var kind: String
    public var hwID: String
    public var name: String
    public var createdAt: Date
    public var configVersion: Int
    public var rotationPending: Bool
    public var rotatedAt: Date?
    public var lastCheckin: KnobCheckin?

    enum CodingKeys: String, CodingKey {
        case id, kind, name
        case hwID = "hw_id"
        case createdAt = "created_at"
        case configVersion = "config_version"
        case rotationPending = "rotation_pending"
        case rotatedAt = "rotated_at"
        case lastCheckin = "last_checkin"
    }

    public init(id: String, kind: String = KnobDevice.knobKind, hwID: String, name: String, createdAt: Date,
                configVersion: Int = 1, rotationPending: Bool = false, rotatedAt: Date? = nil,
                lastCheckin: KnobCheckin? = nil) {
        self.id = id; self.kind = kind; self.hwID = hwID; self.name = name; self.createdAt = createdAt
        self.configVersion = configVersion; self.rotationPending = rotationPending
        self.rotatedAt = rotatedAt; self.lastCheckin = lastCheckin
    }

    public static let knobKind = "cinder-knob"

    public var shortID: String { String(hwID.suffix(6)).uppercased() }

    public func isOnline(now: Date, window: TimeInterval = 150) -> Bool {
        guard let seen = lastCheckin?.seenAt else { return false }
        return now.timeIntervalSince(seen) <= window
    }

    public var supportsStatsIntervals: Bool { Self.firmware(lastCheckin?.fw, atLeast: [0, 7, 0]) }

    public var supportsNowPlaying: Bool { Self.firmware(lastCheckin?.fw, atLeast: [0, 9, 0]) }

    public var supportedPages: [String] {
        AppCatalog.knobDefaultPages + (supportsNowPlaying ? ["nowplaying"] : [])
    }

    static func firmware(_ fw: String?, atLeast min: [Int]) -> Bool {
        guard let fw else { return false }
        let core = fw.prefix { $0.isNumber || $0 == "." }
        let parts = core.split(separator: ".").compactMap { Int($0) }
        guard !parts.isEmpty else { return false }
        for i in 0..<Swift.max(parts.count, min.count) {
            let a = i < parts.count ? parts[i] : 0, b = i < min.count ? min[i] : 0
            if a != b { return a > b }
        }
        return true
    }

    public var configApplied: Bool { (lastCheckin?.appliedVersion ?? 0) >= configVersion }

    public static func current(in devices: [KnobDevice]) -> KnobDevice? {
        devices.filter { $0.kind == knobKind }.max { $0.createdAt < $1.createdAt }
    }
}

struct KnobDeviceList: Decodable {
    let devices: [KnobDevice]
}

public struct MintedKnob: Decodable, Equatable, Sendable {
    public let device: KnobDevice
    public let token: String

    public init(device: KnobDevice, token: String) {
        self.device = device; self.token = token
    }

    public init(from decoder: Decoder) throws {
        device = try KnobDevice(from: decoder)
        token = try decoder.container(keyedBy: Key.self).decode(String.self, forKey: .token)
    }

    enum Key: String, CodingKey { case token }
}

public struct KnobSettings: Codable, Equatable, Sendable {
    public struct Brightness: Codable, Equatable, Sendable {
        public var followEmber: Bool
        public var level: Int
        public var floor: Int
        public var startup: Int
        enum CodingKeys: String, CodingKey {
            case level, floor, startup
            case followEmber = "follow_ember"
        }
        public init(followEmber: Bool, level: Int, floor: Int, startup: Int) {
            self.followEmber = followEmber; self.level = level; self.floor = floor; self.startup = startup
        }
    }

    public struct Page: Codable, Equatable, Sendable, Identifiable {
        public var id: String
        public var on: Bool
        public init(id: String, on: Bool) { self.id = id; self.on = on }
    }

    public struct Display: Codable, Equatable, Sendable {
        public var fastLink: Bool?
        enum CodingKeys: String, CodingKey { case fastLink = "fast_link" }
        public init(fastLink: Bool? = nil) { self.fastLink = fastLink }
    }

    public struct Bot: Codable, Equatable, Sendable {
        public var sleepyAfterS: Int
        public var demoHoldS: Int
        public var sourceLabel: Bool?
        public var workingRing: Bool?
        enum CodingKeys: String, CodingKey {
            case sleepyAfterS = "sleepy_after_s"
            case demoHoldS = "demo_hold_s"
            case sourceLabel = "source_label"
            case workingRing = "working_ring"
        }
        public init(sleepyAfterS: Int, demoHoldS: Int, sourceLabel: Bool? = nil, workingRing: Bool? = nil) {
            self.sleepyAfterS = sleepyAfterS; self.demoHoldS = demoHoldS
            self.sourceLabel = sourceLabel; self.workingRing = workingRing
        }
    }

    public var brightness: Brightness
    public var pages: [Page]
    public var home: String
    public var pollMS: Int
    public var bot: Bot
    public var diagnostics: KnobDiagnostics
    public var statsIntervalS: Int?
    public var liveIntervalS: Int?
    public var display: Display?

    enum CodingKeys: String, CodingKey {
        case brightness, pages, home, bot, diagnostics, display
        case pollMS = "poll_ms"
        case statsIntervalS = "stats_interval_s"
        case liveIntervalS = "live_interval_s"
    }

    public init(brightness: Brightness, pages: [Page], home: String, pollMS: Int, bot: Bot,
                diagnostics: KnobDiagnostics = .off, statsIntervalS: Int? = nil, liveIntervalS: Int? = nil) {
        self.brightness = brightness; self.pages = pages; self.home = home; self.pollMS = pollMS; self.bot = bot
        self.diagnostics = diagnostics
        self.statsIntervalS = statsIntervalS; self.liveIntervalS = liveIntervalS
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        brightness = try c.decode(Brightness.self, forKey: .brightness)
        pages = try c.decode([Page].self, forKey: .pages)
        home = try c.decode(String.self, forKey: .home)
        pollMS = try c.decode(Int.self, forKey: .pollMS)
        bot = try c.decode(Bot.self, forKey: .bot)
        diagnostics = try c.decodeIfPresent(KnobDiagnostics.self, forKey: .diagnostics) ?? .off
        statsIntervalS = try c.decodeIfPresent(Int.self, forKey: .statsIntervalS)
        liveIntervalS = try c.decodeIfPresent(Int.self, forKey: .liveIntervalS)
        display = try c.decodeIfPresent(Display.self, forKey: .display)
    }

    public static let defaults = KnobSettings(
        brightness: Brightness(followEmber: true, level: 153, floor: 10, startup: 153),
        pages: ["bot", "pomodoro", "weather", "nowplaying"].map { Page(id: $0, on: $0 != "nowplaying") },
        home: "bot", pollMS: 2000, bot: Bot(sleepyAfterS: 300, demoHoldS: 20))

    public static let levelRange = 0...255
    public static let floorRange = 1...255
    public static let pollRange = 1000...10000
    public static let sleepyRange = 0...86400
    public static let demoHoldRange = 1...600
    public static let statsIntervals = [30, 60, 120, 300]
    public static let liveIntervals = [2, 5, 10]

    public static func choices(_ allowed: [Int], current: Int?) -> [Int] {
        guard let current, !allowed.contains(current) else { return allowed }
        return (allowed + [current]).sorted()
    }

    public func patch(from old: KnobSettings) -> [String: JSONValue] {
        let new = (try? JSONValue.object(encoding: self)) ?? [:]
        let before = (try? JSONValue.object(encoding: old)) ?? [:]
        return Self.diff(new, before)
    }

    static func diff(_ new: [String: JSONValue], _ old: [String: JSONValue]) -> [String: JSONValue] {
        var out: [String: JSONValue] = [:]
        for (key, n) in new where n != old[key] {
            if case .object(let no) = n, case .object(let oo)? = old[key] {
                out[key] = .object(diff(no, oo))
            } else {
                out[key] = n
            }
        }
        return out
    }

    public func normalized() -> KnobSettings {
        var s = self
        s.brightness.level = s.brightness.level.clamped(to: Self.levelRange)
        s.brightness.startup = s.brightness.startup.clamped(to: Self.levelRange)
        s.brightness.floor = s.brightness.floor.clamped(to: Self.floorRange)
        if s.brightness.floor > s.brightness.level { s.brightness.level = s.brightness.floor }
        if !s.pages.contains(where: \.on), !s.pages.isEmpty { s.pages[0].on = true }
        if !s.pages.contains(where: { $0.id == s.home && $0.on }), let first = s.pages.first(where: \.on) {
            s.home = first.id
        }
        return s
    }

    public func isLastPageOn(_ id: String) -> Bool {
        pages.filter(\.on).map(\.id) == [id]
    }

    public mutating func movePage(_ id: String, by delta: Int) {
        guard let from = pages.firstIndex(where: { $0.id == id }) else { return }
        let to = from + delta
        guard pages.indices.contains(to) else { return }
        pages.swapAt(from, to)
    }

    public mutating func movePage(_ id: String, to target: String) {
        guard id != target,
              let from = pages.firstIndex(where: { $0.id == id }),
              let to = pages.firstIndex(where: { $0.id == target }) else { return }
        pages.move(fromOffsets: [from], toOffset: to > from ? to + 1 : to)
    }
}

extension Comparable {
    func clamped(to r: ClosedRange<Self>) -> Self { min(max(self, r.lowerBound), r.upperBound) }
}
