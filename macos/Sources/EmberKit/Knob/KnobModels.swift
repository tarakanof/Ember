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
    public var diag: KnobDiag?
    public var fwBuild: String?
    public var ota: KnobOTAReport?

    enum CodingKeys: String, CodingKey {
        case fw, ip, rssi, wifi, diag, ota
        case fwBuild = "fw_build"
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

public struct KnobDiag: Codable, Equatable, Sendable {
    public var boots: Int?
    public var crash: KnobCrash?
    public var heapInternalMin: Int?
    public var heapLargestMin: Int?
    public var resetReason: String?
    public var stackFree: [String: Int]?
    public var reboots: Int?
    public var prevResetReason: String?
    public var rebootsSinceSeen: Int?

    enum CodingKeys: String, CodingKey {
        case boots, crash, reboots
        case heapInternalMin = "heap_internal_min"
        case heapLargestMin = "heap_largest_min"
        case resetReason = "reset_reason"
        case stackFree = "stack_free"
        case prevResetReason = "prev_reset_reason"
        case rebootsSinceSeen = "reboots_since_seen"
    }

    public init(boots: Int? = nil, crash: KnobCrash? = nil, heapInternalMin: Int? = nil,
                heapLargestMin: Int? = nil, resetReason: String? = nil, stackFree: [String: Int]? = nil,
                reboots: Int? = nil, prevResetReason: String? = nil, rebootsSinceSeen: Int? = nil) {
        self.boots = boots; self.crash = crash; self.heapInternalMin = heapInternalMin
        self.heapLargestMin = heapLargestMin; self.resetReason = resetReason; self.stackFree = stackFree
        self.reboots = reboots; self.prevResetReason = prevResetReason; self.rebootsSinceSeen = rebootsSinceSeen
    }

    public var unseenRestarts: Int? {
        guard let n = rebootsSinceSeen, n > 1 else { return nil }
        return n
    }

    public var largestBlockMin: Int? {
        guard let n = heapLargestMin, n > 0 else { return nil }
        return n
    }
}

public struct KnobOTAReport: Codable, Equatable, Sendable {
    public var image: String?
    public var phase: String?
    public var rollback: Bool?
    public var slot: Int?

    public init(image: String? = nil, phase: String? = nil, rollback: Bool? = nil, slot: Int? = nil) {
        self.image = image; self.phase = phase; self.rollback = rollback; self.slot = slot
    }
}

public struct KnobCrash: Codable, Equatable, Sendable {
    public var elf: String?
    public var id: String?
    public var size: Int?
    public var pc: String?
    public var reason: String?
    public var task: String?

    public init(id: String? = nil, size: Int? = nil, pc: String? = nil, reason: String? = nil, task: String? = nil,
                elf: String? = nil) {
        self.id = id; self.size = size; self.pc = pc; self.reason = reason; self.task = task; self.elf = elf
    }
}

public struct KnobCaps: Codable, Equatable, Sendable {
    public struct Limits: Codable, Equatable, Sendable {
        public var viewBytes: Int?
        public var configBytes: Int?

        enum CodingKeys: String, CodingKey {
            case viewBytes = "view_bytes"
            case configBytes = "config_bytes"
        }

        public init(viewBytes: Int? = nil, configBytes: Int? = nil) {
            self.viewBytes = viewBytes; self.configBytes = configBytes
        }
    }

    public var view: [Int]
    public var pages: [String]
    public var features: [String]
    public var limits: Limits?
    public var rotations: [Int]?
    public var source: String?
    public var capsError: String?

    enum CodingKeys: String, CodingKey {
        case view, pages, features, limits, rotations, source
        case capsError = "caps_error"
    }

    public init(view: [Int] = [1, 1], pages: [String], features: [String] = [], limits: Limits? = nil,
                rotations: [Int]? = nil, source: String? = nil, capsError: String? = nil) {
        self.view = view; self.pages = pages; self.features = features; self.limits = limits
        self.rotations = rotations; self.source = source
        self.capsError = capsError
    }

    public static let statsIntervals = "stats_intervals"
    public static let nowPlayingPage = "nowplaying"

    static let legacyFeatureFloors: [String: [Int]] = [statsIntervals: [0, 7, 0]]
    static let legacyPageFloors: [String: [Int]] = [nowPlayingPage: [0, 9, 0]]
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
    public var effectiveCaps: KnobCaps?

    enum CodingKeys: String, CodingKey {
        case id, kind, name
        case hwID = "hw_id"
        case createdAt = "created_at"
        case configVersion = "config_version"
        case rotationPending = "rotation_pending"
        case rotatedAt = "rotated_at"
        case lastCheckin = "last_checkin"
        case effectiveCaps = "effective_caps"
    }

    public init(id: String, kind: String = KnobDevice.knobKind, hwID: String, name: String, createdAt: Date,
                configVersion: Int = 1, rotationPending: Bool = false, rotatedAt: Date? = nil,
                lastCheckin: KnobCheckin? = nil, effectiveCaps: KnobCaps? = nil) {
        self.id = id; self.kind = kind; self.hwID = hwID; self.name = name; self.createdAt = createdAt
        self.configVersion = configVersion; self.rotationPending = rotationPending
        self.rotatedAt = rotatedAt; self.lastCheckin = lastCheckin; self.effectiveCaps = effectiveCaps
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        kind = try c.decode(String.self, forKey: .kind)
        hwID = try c.decode(String.self, forKey: .hwID)
        name = try c.decode(String.self, forKey: .name)
        createdAt = try c.decode(Date.self, forKey: .createdAt)
        configVersion = try c.decode(Int.self, forKey: .configVersion)
        rotationPending = try c.decode(Bool.self, forKey: .rotationPending)
        rotatedAt = try c.decodeIfPresent(Date.self, forKey: .rotatedAt)
        lastCheckin = try c.decodeIfPresent(KnobCheckin.self, forKey: .lastCheckin)
        effectiveCaps = (try? c.decodeIfPresent(KnobCaps.self, forKey: .effectiveCaps)) ?? nil
    }

    public static let knobKind = "cinder-knob"
    public static let clockKind = "awtrix-ng"

    public var shortID: String { String(hwID.suffix(6)).uppercased() }

    public func isOnline(now: Date, window: TimeInterval = 150) -> Bool {
        guard let seen = lastCheckin?.seenAt else { return false }
        return now.timeIntervalSince(seen) <= window
    }

    public func supports(feature: String) -> Bool {
        if let caps = effectiveCaps { return caps.features.contains(feature) }
        guard let floor = KnobCaps.legacyFeatureFloors[feature] else { return false }
        return Self.firmware(lastCheckin?.fw, atLeast: floor)
    }

    public func supports(page: String) -> Bool {
        if let caps = effectiveCaps { return caps.pages.contains(page) }
        if AppCatalog.knobDefaultPages.contains(page) { return true }
        guard let floor = KnobCaps.legacyPageFloors[page] else { return false }
        return Self.firmware(lastCheckin?.fw, atLeast: floor)
    }

    public var supportedRotations: [Int] {
        let listed = (effectiveCaps?.rotations ?? []).filter { KnobSettings.rotations.contains($0) }
        return Set(listed + [0]).sorted()
    }

    public func rotationChoices(current: Int?) -> [Int] {
        guard let current, supportedRotations.count > 1 else { return [] }
        return KnobSettings.choices(supportedRotations, current: current)
    }

    public var supportedPages: [String] {
        if let caps = effectiveCaps { return caps.pages }
        return AppCatalog.knobDefaultPages + KnobCaps.legacyPageFloors.keys.sorted().filter { supports(page: $0) }
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
        return !fw.dropFirst(core.count).hasPrefix("-")
    }

    public var configApplied: Bool { (lastCheckin?.appliedVersion ?? 0) >= configVersion }

    public static func current(in devices: [KnobDevice]) -> KnobDevice? {
        devices.filter { $0.kind == knobKind }.max { $0.createdAt < $1.createdAt }
    }

    public static func clock(in devices: [KnobDevice]) -> KnobDevice? {
        devices.filter { $0.kind == clockKind }.min { $0.id < $1.id }
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

    public struct Quiet: Codable, Equatable, Sendable {
        public var calm: Bool
        public var dimLevel: Int
        enum CodingKeys: String, CodingKey {
            case calm
            case dimLevel = "dim_level"
        }
        public init(calm: Bool, dimLevel: Int) { self.calm = calm; self.dimLevel = dimLevel }
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
    public var quiet: Quiet?
    public var rotation: Int?

    enum CodingKeys: String, CodingKey {
        case brightness, pages, home, bot, diagnostics, display, quiet, rotation
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
        quiet = try c.decodeIfPresent(Quiet.self, forKey: .quiet)
        rotation = try c.decodeIfPresent(Int.self, forKey: .rotation)
    }

    public static let defaults = KnobSettings(
        brightness: Brightness(followEmber: true, level: 153, floor: 10, startup: 153),
        pages: ["bot", "pomodoro", "weather", "nowplaying"].map { Page(id: $0, on: $0 != "nowplaying") },
        home: "bot", pollMS: 2000, bot: Bot(sleepyAfterS: 300, demoHoldS: 20))

    public static let levelRange = 0...255
    public static let floorRange = 1...255
    public static let quietDimRange = 1...255
    public static let pollRange = 1000...10000
    public static let sleepyRange = 0...86400
    public static let demoHoldRange = 1...600
    public static let statsIntervals = [30, 60, 120, 300]
    public static let liveIntervals = [2, 5, 10]
    public static let rotations = [0, 90, 180, 270]

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
        if let dim = s.quiet?.dimLevel { s.quiet?.dimLevel = dim.clamped(to: Self.quietDimRange) }
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
