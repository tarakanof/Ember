import Foundation

/// The knob's last checkin as the server stores it (`deviceCheckin` in
/// cmd/ember/devices.go).
public struct KnobCheckin: Codable, Equatable, Sendable {
    public var seenAt: Date
    public var fw: String
    public var ip: String
    public var rssi: Int
    public var heapInternalFree: Int
    public var heapInternalLargest: Int
    public var uptimeS: Int
    public var appliedVersion: Int

    enum CodingKeys: String, CodingKey {
        case fw, ip, rssi
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

/// One registered device (`deviceView`): no secrets.
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

    /// The 6-hex short ID the knob shows on its setup face.
    public var shortID: String { String(hwID.suffix(6)).uppercased() }

    /// Checked in within `window` (two missed 60 s checkins).
    public func isOnline(now: Date, window: TimeInterval = 150) -> Bool {
        guard let seen = lastCheckin?.seenAt else { return false }
        return now.timeIntervalSince(seen) <= window
    }

    /// The knob runs the latest config.
    public var configApplied: Bool { (lastCheckin?.appliedVersion ?? 0) >= configVersion }

    /// The single knob the app shows: the most recently registered one.
    public static func current(in devices: [KnobDevice]) -> KnobDevice? {
        devices.filter { $0.kind == knobKind }.max { $0.createdAt < $1.createdAt }
    }
}

struct KnobDeviceList: Decodable {
    let devices: [KnobDevice]
}

/// `POST /v1/devices`: the record plus its token, shown only here.
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

/// The knob's settings (`knobSettings`, schema v1).
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

    public struct Bot: Codable, Equatable, Sendable {
        public var sleepyAfterS: Int
        public var demoHoldS: Int
        enum CodingKeys: String, CodingKey {
            case sleepyAfterS = "sleepy_after_s"
            case demoHoldS = "demo_hold_s"
        }
        public init(sleepyAfterS: Int, demoHoldS: Int) {
            self.sleepyAfterS = sleepyAfterS; self.demoHoldS = demoHoldS
        }
    }

    public var brightness: Brightness
    public var pages: [Page]
    public var home: String
    public var pollMS: Int
    public var bot: Bot
    /// What the knob reports for the Dashboard; a pre-#239 server has no
    /// such field and reads as off.
    public var diagnostics: KnobDiagnostics

    enum CodingKeys: String, CodingKey {
        case brightness, pages, home, bot, diagnostics
        case pollMS = "poll_ms"
    }

    public init(brightness: Brightness, pages: [Page], home: String, pollMS: Int, bot: Bot,
                diagnostics: KnobDiagnostics = .off) {
        self.brightness = brightness; self.pages = pages; self.home = home; self.pollMS = pollMS; self.bot = bot
        self.diagnostics = diagnostics
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        brightness = try c.decode(Brightness.self, forKey: .brightness)
        pages = try c.decode([Page].self, forKey: .pages)
        home = try c.decode(String.self, forKey: .home)
        pollMS = try c.decode(Int.self, forKey: .pollMS)
        bot = try c.decode(Bot.self, forKey: .bot)
        diagnostics = try c.decodeIfPresent(KnobDiagnostics.self, forKey: .diagnostics) ?? .off
    }

    /// The server's defaults (`defaultKnobSettings`).
    public static let defaults = KnobSettings(
        brightness: Brightness(followEmber: true, level: 153, floor: 10, startup: 153),
        pages: ["bot", "pomodoro", "weather"].map { Page(id: $0, on: true) },
        home: "bot", pollMS: 2000, bot: Bot(sleepyAfterS: 300, demoHoldS: 20))

    public static let levelRange = 0...255
    public static let floorRange = 1...255
    public static let pollRange = 1000...10000
    public static let sleepyRange = 0...86400
    public static let demoHoldRange = 1...600

    /// The changed fields as a merge-PUT body: nested objects carry only
    /// their changed fields, `pages` goes whole.
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

    /// Keeps the settings valid after an edit, the way the server checks
    /// them: floor ≤ level, the home page on, at least one page on.
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

    /// Whether turning page `id` off would leave nothing on.
    public func isLastPageOn(_ id: String) -> Bool {
        pages.filter(\.on).map(\.id) == [id]
    }

    /// Moves page `id` one step (`-1` up, `1` down). No-op at the ends or for an unknown id.
    public mutating func movePage(_ id: String, by delta: Int) {
        guard let from = pages.firstIndex(where: { $0.id == id }) else { return }
        let to = from + delta
        guard pages.indices.contains(to) else { return }
        pages.swapAt(from, to)
    }

    /// Moves page `id` to where page `target` is (drag and drop). The home page is untouched.
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
