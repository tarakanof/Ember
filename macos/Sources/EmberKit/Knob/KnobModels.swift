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
    public static let wxMoments = "wx_moments"
    public static let idleDim = "idle_dim"
    public static let pomoColors = "pomo_colors"
    public static let wxFaceList = "wx_face_list"
    public static let npDim = "np_dim"
    public static let demo = "demo"
    public static let nowPlayingPage = "nowplaying"

    static let legacyFeatureFloors: [String: [Int]] = [statsIntervals: [0, 7, 0]]
    static let swipePagesFloor = [0, 9, 15]
    static let legacyPageFloors: [String: [Int]] = [nowPlayingPage: [0, 9, 0]]
}

public struct KnobRotationChoice: Equatable, Hashable, Sendable {
    public let degrees: Int
    public let supported: Bool

    public init(degrees: Int, supported: Bool) { self.degrees = degrees; self.supported = supported }
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
    public var shown: KnobShown?

    enum CodingKeys: String, CodingKey {
        case id, kind, name, shown
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
                lastCheckin: KnobCheckin? = nil, effectiveCaps: KnobCaps? = nil, shown: KnobShown? = nil) {
        self.id = id; self.kind = kind; self.hwID = hwID; self.name = name; self.createdAt = createdAt
        self.configVersion = configVersion; self.rotationPending = rotationPending
        self.rotatedAt = rotatedAt; self.lastCheckin = lastCheckin; self.effectiveCaps = effectiveCaps
        self.shown = shown
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
        shown = (try? c.decodeIfPresent(KnobShown.self, forKey: .shown)) ?? nil
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

    public func rotationChoices(current: Int?) -> [KnobRotationChoice] {
        guard let current else { return [] }
        let supported = supportedRotations
        guard supported.count > 1 || current != 0 else { return [] }
        var out = supported.map { KnobRotationChoice(degrees: $0, supported: true) }
        if !supported.contains(current) { out.append(KnobRotationChoice(degrees: current, supported: false)) }
        return out.sorted { $0.degrees < $1.degrees }
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

    public var supportsSwipePages: Bool { Self.firmware(lastCheckin?.fw, atLeast: KnobCaps.swipePagesFloor) }

    public var configApplied: Bool { (lastCheckin?.appliedVersion ?? 0) >= configVersion }

    public static func current(in devices: [KnobDevice]) -> KnobDevice? {
        devices.filter { $0.kind == knobKind }.max { $0.createdAt < $1.createdAt }
    }

    public static func clock(in devices: [KnobDevice]) -> KnobDevice? {
        devices.filter { $0.kind == clockKind }.min { $0.id < $1.id }
    }
}

public struct KnobShown: Codable, Equatable, Sendable {
    public var page: String
    public var face: String?
    public var at: Date

    public init(page: String, face: String? = nil, at: Date) { self.page = page; self.face = face; self.at = at }
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
        public var rotation: Int?
        enum CodingKeys: String, CodingKey {
            case rotation
            case fastLink = "fast_link"
        }
        public init(fastLink: Bool? = nil, rotation: Int? = nil) { self.fastLink = fastLink; self.rotation = rotation }
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

    public struct Idle: Codable, Equatable, Sendable {
        public var dimAfterS: Int?
        public var dimLevel: Int?
        public var offAfterS: Int?
        enum CodingKeys: String, CodingKey {
            case dimAfterS = "dim_after_s"
            case dimLevel = "dim_level"
            case offAfterS = "off_after_s"
        }
        public init(dimAfterS: Int? = nil, dimLevel: Int? = nil, offAfterS: Int? = nil) {
            self.dimAfterS = dimAfterS; self.dimLevel = dimLevel; self.offAfterS = offAfterS
        }
        var isEmpty: Bool { dimAfterS == nil && dimLevel == nil && offAfterS == nil }
    }

    public struct Apps: Codable, Equatable, Sendable {
        public struct Pomodoro: Codable, Equatable, Sendable {
            public var focusColor: String?
            public var breakColor: String?
            enum CodingKeys: String, CodingKey {
                case focusColor = "focus_color"
                case breakColor = "break_color"
            }
            public init(focusColor: String? = nil, breakColor: String? = nil) {
                self.focusColor = focusColor; self.breakColor = breakColor
            }
        }
        public struct Weather: Codable, Equatable, Sendable {
            public var faces: [String]?
            public init(faces: [String]? = nil) { self.faces = faces }
        }
        public struct NowPlaying: Codable, Equatable, Sendable {
            public var dimAfterS: Int?
            enum CodingKeys: String, CodingKey { case dimAfterS = "dim_after_s" }
            public init(dimAfterS: Int? = nil) { self.dimAfterS = dimAfterS }
        }

        public var pomodoro: Pomodoro?
        public var weather: Weather?
        public var nowplaying: NowPlaying?

        public init(pomodoro: Pomodoro? = nil, weather: Weather? = nil, nowplaying: NowPlaying? = nil) {
            self.pomodoro = pomodoro; self.weather = weather; self.nowplaying = nowplaying
        }

        var isEmpty: Bool { pomodoro == nil && weather == nil && nowplaying == nil }
    }

    public struct Bot: Codable, Equatable, Sendable {
        public var sleepyAfterS: Int
        public var demoHoldS: Int
        public var sourceLabel: Bool?
        public var workingRing: Bool?
        public var weatherMoments: Bool?
        public var weatherEveryMin: Int?
        enum CodingKeys: String, CodingKey {
            case sleepyAfterS = "sleepy_after_s"
            case demoHoldS = "demo_hold_s"
            case sourceLabel = "source_label"
            case workingRing = "working_ring"
            case weatherMoments = "weather_moments"
            case weatherEveryMin = "weather_every_min"
        }
        public init(sleepyAfterS: Int, demoHoldS: Int, sourceLabel: Bool? = nil, workingRing: Bool? = nil,
                    weatherMoments: Bool? = nil, weatherEveryMin: Int? = nil) {
            self.sleepyAfterS = sleepyAfterS; self.demoHoldS = demoHoldS
            self.sourceLabel = sourceLabel; self.workingRing = workingRing
            self.weatherMoments = weatherMoments; self.weatherEveryMin = weatherEveryMin
        }
    }

    public var brightness: Brightness
    public var pages: [Page]
    public var home: String
    public var swipePages: Bool?
    public var pollMS: Int
    public var bot: Bot
    public var diagnostics: KnobDiagnostics
    public var statsIntervalS: Int?
    public var liveIntervalS: Int?
    public var display: Display?
    public var quiet: Quiet?
    public var idle: Idle?
    public var apps: Apps?

    enum CodingKeys: String, CodingKey {
        case brightness, pages, home, bot, diagnostics, display, quiet, idle, apps
        case pollMS = "poll_ms"
        case swipePages = "swipe_pages"
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
        swipePages = try c.decodeIfPresent(Bool.self, forKey: .swipePages)
        pollMS = try c.decode(Int.self, forKey: .pollMS)
        bot = try c.decode(Bot.self, forKey: .bot)
        diagnostics = try c.decodeIfPresent(KnobDiagnostics.self, forKey: .diagnostics) ?? .off
        statsIntervalS = try c.decodeIfPresent(Int.self, forKey: .statsIntervalS)
        liveIntervalS = try c.decodeIfPresent(Int.self, forKey: .liveIntervalS)
        display = try c.decodeIfPresent(Display.self, forKey: .display)
        quiet = try c.decodeIfPresent(Quiet.self, forKey: .quiet)
        idle = try c.decodeIfPresent(Idle.self, forKey: .idle)
        apps = try c.decodeIfPresent(Apps.self, forKey: .apps)
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
    public static let weatherEveryRange = 0...240
    public static let weatherEveryFloor = 30
    public static let weatherEveryStep = 5

    public static func weatherEveryShown(_ minutes: Int) -> Int {
        minutes <= 0 ? 0 : max(minutes, weatherEveryFloor).clamped(to: weatherEveryRange)
    }

    public static func weatherEveryStepped(from current: Int, to proposed: Int) -> Int {
        if proposed <= 0 { return 0 }
        if proposed < weatherEveryFloor { return proposed < current && current <= weatherEveryFloor ? 0 : weatherEveryFloor }
        return (proposed.clamped(to: weatherEveryRange) / weatherEveryStep) * weatherEveryStep
    }
    public static let idleDimDefault = 20
    public static let idleDimLevelRange = 1...255
    public static let idleDimAfterSteps = [60, 300, 600, 1800, 3600]
    public static let idleOffAfterSteps = [600, 1800, 3600, 7200, 14400]
    public static let nowPlayingDimDefault = 180
    public static let nowPlayingDimRange = 60...1800
    public static let nowPlayingDimChoices = [60, 180, 300, 600, 1800]
    public static let weatherFaceIDs = KnobWeatherFaceID.allCases.map(\.rawValue)
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
        return Self.diff(new, before, clearing: false)
    }

    static let v2Keys: Set<String> = ["idle", "apps"]

    static func diff(_ new: [String: JSONValue], _ old: [String: JSONValue], clearing: Bool = false) -> [String: JSONValue] {
        var out: [String: JSONValue] = [:]
        for (key, n) in new where n != old[key] {
            if case .object(let no) = n, case .object(let oo)? = old[key] {
                out[key] = .object(diff(no, oo, clearing: clearing || v2Keys.contains(key)))
            } else {
                out[key] = n
            }
        }
        for key in old.keys where new[key] == nil && (clearing || v2Keys.contains(key)) { out[key] = .null }
        return out
    }

    public func normalized() -> KnobSettings {
        var s = self
        s.brightness.level = s.brightness.level.clamped(to: Self.levelRange)
        s.brightness.startup = s.brightness.startup.clamped(to: Self.levelRange)
        s.brightness.floor = s.brightness.floor.clamped(to: Self.floorRange)
        if s.brightness.floor > s.brightness.level { s.brightness.level = s.brightness.floor }
        if let dim = s.quiet?.dimLevel { s.quiet?.dimLevel = dim.clamped(to: Self.quietDimRange) }
        if let every = s.bot.weatherEveryMin { s.bot.weatherEveryMin = every.clamped(to: Self.weatherEveryRange) }
        s.normalizeV2()
        if !s.pages.contains(where: \.on), !s.pages.isEmpty { s.pages[0].on = true }
        if !s.pages.contains(where: { $0.id == s.home && $0.on }), let first = s.pages.first(where: \.on) {
            s.home = first.id
        }
        return s
    }

    public func showsSwipeToggle(for knob: KnobDevice?) -> Bool {
        swipePages != nil && knob?.supportsSwipePages == true
    }

    public func showsSwipeFooter(for knob: KnobDevice?) -> Bool {
        showsSwipeToggle(for: knob) && swipePages == true
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

    mutating func normalizeV2() {
        if var i = idle {
            if let v = i.dimLevel { i.dimLevel = v.clamped(to: Self.idleDimLevelRange) }
            if let d = i.dimAfterS, let o = i.offAfterS, d > 0, o > 0, o <= d { i.offAfterS = nil }
            idle = i.isEmpty ? nil : i
        }
        if var a = apps {
            if a.pomodoro?.focusColor == nil, a.pomodoro?.breakColor == nil { a.pomodoro = nil }
            if let v = a.nowplaying?.dimAfterS { a.nowplaying?.dimAfterS = v.clamped(to: Self.nowPlayingDimRange) }
            if a.nowplaying?.dimAfterS == nil { a.nowplaying = nil }
            if var f = a.weather?.faces {
                var seen = Set<String>()
                f = f.filter { Self.weatherFaceIDs.contains($0) && seen.insert($0).inserted }
                a.weather = f.isEmpty ? nil : .init(faces: f)
            } else {
                a.weather = nil
            }
            apps = a
        }
    }

    public var weatherFacesListed: [String] { apps?.weather?.faces ?? Self.weatherFaceIDs }

    public var weatherFaceRows: [(id: String, on: Bool)] {
        let listed = weatherFacesListed
        return listed.map { ($0, true) } + Self.weatherFaceIDs.filter { !listed.contains($0) }.map { ($0, false) }
    }

    public mutating func setWeatherFaces(rows: [(id: String, on: Bool)]) {
        let faces = rows.filter { $0.on }.map(\.id)
        guard !faces.isEmpty else { return }
        if apps == nil { apps = Apps() }
        apps?.weather = .init(faces: faces)
    }

    public mutating func setWeatherFace(_ id: String, on: Bool) {
        var rows = weatherFaceRows
        guard let i = rows.firstIndex(where: { $0.id == id }) else { return }
        rows[i].on = on
        setWeatherFaces(rows: rows)
    }

    public func canMoveWeatherFace(_ id: String, by delta: Int) -> Bool {
        let listed = weatherFacesListed
        guard let from = listed.firstIndex(of: id) else { return false }
        return listed.indices.contains(from + delta)
    }

    public mutating func moveWeatherFace(_ id: String, by delta: Int) {
        guard canMoveWeatherFace(id, by: delta) else { return }
        var rows = weatherFaceRows
        guard let from = rows.firstIndex(where: { $0.id == id }) else { return }
        rows.swapAt(from, from + delta)
        setWeatherFaces(rows: rows)
    }

    public mutating func editIdle(_ change: (inout Idle) -> Void) {
        var i = idle ?? Idle()
        change(&i)
        idle = i
    }

    public mutating func editPomodoro(_ change: (inout Apps.Pomodoro) -> Void) {
        var p = apps?.pomodoro ?? .init()
        change(&p)
        if apps == nil { apps = Apps() }
        apps?.pomodoro = p
    }

    public mutating func setNowPlayingDim(_ seconds: Int) {
        if apps == nil { apps = Apps() }
        apps?.nowplaying = .init(dimAfterS: seconds)
    }

    public static func idleDimAfterChoices(off: Int?, current: Int?) -> [Int] {
        let o = off ?? 0
        let allowed = idleDimAfterSteps.filter { o == 0 || $0 < o }
        return [0] + choices(allowed, current: current.flatMap { $0 > 0 ? $0 : nil })
    }

    public static func idleOffAfterChoices(dim: Int?, current: Int?) -> [Int] {
        let d = dim ?? 0
        let allowed = idleOffAfterSteps.filter { $0 > d }
        return [0] + choices(allowed, current: current.flatMap { $0 > 0 ? $0 : nil })
    }

    public func previewTheme(for knob: KnobDevice?) -> KnobTheme {
        var t = KnobTheme.standard
        if knob?.supports(feature: KnobCaps.pomoColors) == true {
            if let c = apps?.pomodoro?.focusColor.flatMap(RGB.init(hex:)) { t.pomodoro.colors.focus = c }
            if let c = apps?.pomodoro?.breakColor.flatMap(RGB.init(hex:)) { t.pomodoro.colors.break = c }
        }
        if knob?.supports(feature: KnobCaps.wxFaceList) == true {
            t.weather.dots.listed = weatherFacesListed.compactMap(KnobWeatherFaceID.init(rawValue:))
        }
        return t
    }
}

extension Comparable {
    func clamped(to r: ClosedRange<Self>) -> Self { min(max(self, r.lowerBound), r.upperBound) }
}
