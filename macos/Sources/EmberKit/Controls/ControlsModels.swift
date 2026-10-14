import Foundation

public enum ControlGesture: String, Codable, CaseIterable, Sendable {
    case push, doublePush = "double_push", longPush = "long_push", turn

    public var isTurn: Bool { self == .turn }
}

public enum ControlKind: String, Codable, CaseIterable, Sendable {
    case none, media, mute, shortcut, applescript, volume, scroll

    public static let pressKinds: [ControlKind] = [.none, .media, .mute, .shortcut, .applescript]
    public static let turnKinds: [ControlKind] = [.none, .volume, .scroll]

    public var needsEventAccess: Bool { self == .media || self == .scroll }

    public init(from decoder: Decoder) throws {
        self = ControlKind(rawValue: try decoder.singleValueContainer().decode(String.self)) ?? .none
    }
}

public enum MediaKey: String, Codable, CaseIterable, Sendable {
    case playPause = "play_pause", next, previous
}

public enum ScrollAxis: String, Codable, CaseIterable, Sendable {
    case vertical, horizontal
}

public struct ControlAction: Codable, Equatable, Sendable {
    public var kind: ControlKind
    public var key: MediaKey?
    public var name: String?
    public var script: String?
    public var step: Int?
    public var lines: Int?
    public var axis: ScrollAxis?

    public init(kind: ControlKind = .none, key: MediaKey? = nil, name: String? = nil, script: String? = nil,
                step: Int? = nil, lines: Int? = nil, axis: ScrollAxis? = nil) {
        self.kind = kind; self.key = key; self.name = name; self.script = script
        self.step = step; self.lines = lines; self.axis = axis
    }

    public func switched(to kind: ControlKind) -> ControlAction {
        switch kind {
        case .media: ControlAction(kind: kind, key: key ?? .playPause)
        case .shortcut: ControlAction(kind: kind, name: name ?? "")
        case .applescript: ControlAction(kind: kind, script: script ?? "")
        case .volume: ControlAction(kind: kind, step: step ?? 2)
        case .scroll: ControlAction(kind: kind, lines: lines ?? 3, axis: axis ?? .vertical)
        case .none, .mute: ControlAction(kind: kind)
        }
    }
}

public struct ControlsMac: Codable, Equatable, Sendable, Identifiable {
    public let name: String
    public let listening: Bool
    public let lastPoll: String?

    public var id: String { name }

    public init(name: String, listening: Bool, lastPoll: String? = nil) {
        self.name = name; self.listening = listening; self.lastPoll = lastPoll
    }

    enum CodingKeys: String, CodingKey { case name, listening, lastPoll = "last_poll" }
}

public struct ControlsMapping: Codable, Equatable, Sendable {
    public var push: ControlAction
    public var doublePush: ControlAction
    public var longPush: ControlAction
    public var turn: ControlAction

    public init(push: ControlAction = ControlAction(kind: .media, key: .playPause),
                doublePush: ControlAction = ControlAction(), longPush: ControlAction = ControlAction(),
                turn: ControlAction = ControlAction(kind: .volume, step: 2)) {
        self.push = push; self.doublePush = doublePush; self.longPush = longPush; self.turn = turn
    }

    public subscript(gesture: ControlGesture) -> ControlAction {
        get {
            switch gesture {
            case .push: push
            case .doublePush: doublePush
            case .longPush: longPush
            case .turn: turn
            }
        }
        set {
            switch gesture {
            case .push: push = newValue
            case .doublePush: doublePush = newValue
            case .longPush: longPush = newValue
            case .turn: turn = newValue
            }
        }
    }

    public var needsEventAccess: Bool {
        ControlGesture.allCases.contains { self[$0].kind.needsEventAccess }
    }

    public static let defaultsKey = "knobControls.mapping"

    public static func load(from defaults: UserDefaults) -> ControlsMapping {
        guard let data = defaults.data(forKey: defaultsKey),
              let m = try? JSONDecoder().decode(ControlsMapping.self, from: data) else { return ControlsMapping() }
        return m
    }

    public func save(to defaults: UserDefaults) {
        if let data = try? JSONEncoder().encode(self) { defaults.set(data, forKey: Self.defaultsKey) }
    }
}

public struct ControlsConfig: Codable, Equatable, Sendable {
    public var target: String
    public var macs: [ControlsMac]

    public init(target: String = "", macs: [ControlsMac] = []) {
        self.target = target; self.macs = macs
    }

    enum CodingKeys: String, CodingKey { case target, macs }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        target = try c.decodeIfPresent(String.self, forKey: .target) ?? ""
        macs = try c.decodeIfPresent([ControlsMac].self, forKey: .macs) ?? []
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(target, forKey: .target)
    }

    public func targetChoices(thisMac: String) -> [String] {
        var names = macs.map(\.name)
        for extra in [thisMac, target] where !extra.isEmpty && !names.contains(extra) { names.append(extra) }
        return names.sorted()
    }
}

public enum ControlsGate {
    public static func shouldListen(target: String?, thisMac: String, hasToken: Bool) -> Bool {
        guard hasToken, let target, !target.isEmpty else { return false }
        return target == thisMac
    }
}

public struct ControlCommandRejected: Error, Equatable {
    public let reason: String
}

public struct ControlCommand: Decodable, Equatable, Sendable {
    public static let lifetimeTurn: Duration = .seconds(2)
    public static let lifetimePress: Duration = .seconds(5)
    public static let maxSteps = 100

    public let id: String
    public let gesture: ControlGesture
    public let steps: Int
    public let ageMs: Int

    public init(id: String, gesture: ControlGesture, steps: Int = 0, ageMs: Int = 0) {
        self.id = id; self.gesture = gesture; self.steps = steps; self.ageMs = ageMs
    }

    public var lifetime: Duration { gesture == .turn ? Self.lifetimeTurn : Self.lifetimePress }

    enum CodingKeys: String, CodingKey, CaseIterable { case id, gesture, steps, ageMs = "age_ms" }

    private struct AnyKey: CodingKey {
        let stringValue: String
        var intValue: Int? { nil }
        init?(stringValue: String) { self.stringValue = stringValue }
        init?(intValue: Int) { nil }
    }

    public init(from decoder: Decoder) throws {
        let allowed = Set(CodingKeys.allCases.map(\.rawValue))
        let extra = try decoder.container(keyedBy: AnyKey.self).allKeys.map(\.stringValue).filter { !allowed.contains($0) }
        guard extra.isEmpty else { throw ControlCommandRejected(reason: "unexpected fields \(extra.sorted())") }
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        let raw = try c.decode(String.self, forKey: .gesture)
        guard let g = ControlGesture(rawValue: raw) else { throw ControlCommandRejected(reason: "unknown gesture") }
        gesture = g
        steps = try c.decodeIfPresent(Int.self, forKey: .steps) ?? 0
        guard g == .turn ? (steps != 0 && (-Self.maxSteps...Self.maxSteps).contains(steps)) : steps == 0 else {
            throw ControlCommandRejected(reason: "bad steps")
        }
        ageMs = max(0, try c.decodeIfPresent(Int.self, forKey: .ageMs) ?? 0)
    }
}

public struct ControlCommands: Decodable, Equatable, Sendable {
    public let commands: [ControlCommand]

    enum CodingKeys: String, CodingKey { case commands }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        commands = try c.decode([ControlCommand].self, forKey: .commands)
    }
}

public enum ThisMac {
    public static func name(source: String, computerName: String) -> String {
        let s = source.trimmingCharacters(in: .whitespaces)
        return MusicPlayerInfo.clip(s.isEmpty ? computerName : s, MusicPlayerInfo.playerNameLimit)
    }
}
