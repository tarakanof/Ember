import Foundation

public enum ControlGesture: String, CaseIterable, Sendable {
    case push, doublePush = "double_push", longPush = "long_push", turn

    public var isTurn: Bool { self == .turn }
}

public enum ControlKind: String, Codable, CaseIterable, Sendable {
    case none, media, mute, shortcut, applescript, volume, scroll

    public static let pressKinds: [ControlKind] = [.none, .media, .mute, .shortcut, .applescript]
    public static let turnKinds: [ControlKind] = [.none, .volume, .scroll]

    public var needsEventAccess: Bool { self == .media || self == .scroll }

    public var lifetime: Duration { self == .volume || self == .scroll ? .seconds(2) : .seconds(5) }

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

public struct ControlsConfig: Codable, Equatable, Sendable {
    public var target: String
    public var push: ControlAction
    public var doublePush: ControlAction
    public var longPush: ControlAction
    public var turn: ControlAction
    public var macs: [ControlsMac]

    public init(target: String = "", push: ControlAction = ControlAction(kind: .media, key: .playPause),
                doublePush: ControlAction = ControlAction(), longPush: ControlAction = ControlAction(),
                turn: ControlAction = ControlAction(kind: .volume, step: 2), macs: [ControlsMac] = []) {
        self.target = target; self.push = push; self.doublePush = doublePush; self.longPush = longPush
        self.turn = turn; self.macs = macs
    }

    enum CodingKeys: String, CodingKey {
        case target, push, doublePush = "double_push", longPush = "long_push", turn, macs
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let d = ControlsConfig()
        target = try c.decodeIfPresent(String.self, forKey: .target) ?? d.target
        push = try c.decodeIfPresent(ControlAction.self, forKey: .push) ?? d.push
        doublePush = try c.decodeIfPresent(ControlAction.self, forKey: .doublePush) ?? d.doublePush
        longPush = try c.decodeIfPresent(ControlAction.self, forKey: .longPush) ?? d.longPush
        turn = try c.decodeIfPresent(ControlAction.self, forKey: .turn) ?? d.turn
        macs = try c.decodeIfPresent([ControlsMac].self, forKey: .macs) ?? []
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(target, forKey: .target)
        try c.encode(push, forKey: .push)
        try c.encode(doublePush, forKey: .doublePush)
        try c.encode(longPush, forKey: .longPush)
        try c.encode(turn, forKey: .turn)
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

    public func targetChoices(thisMac: String) -> [String] {
        var names = macs.map(\.name)
        for extra in [thisMac, target] where !extra.isEmpty && !names.contains(extra) { names.append(extra) }
        return names.sorted()
    }
}

public struct ControlCommand: Decodable, Equatable, Sendable {
    public let id: String
    public let kind: ControlKind
    public let key: MediaKey?
    public let name: String
    public let script: String
    public let axis: ScrollAxis?
    public let delta: Int
    public let ageMs: Int

    public init(id: String, kind: ControlKind, key: MediaKey? = nil, name: String = "", script: String = "",
                axis: ScrollAxis? = nil, delta: Int = 0, ageMs: Int = 0) {
        self.id = id; self.kind = kind; self.key = key; self.name = name; self.script = script
        self.axis = axis; self.delta = delta; self.ageMs = ageMs
    }

    enum CodingKeys: String, CodingKey { case id, kind, key, name, script, axis, delta, ageMs = "age_ms" }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        kind = try c.decodeIfPresent(ControlKind.self, forKey: .kind) ?? .none
        key = try c.decodeIfPresent(String.self, forKey: .key).flatMap(MediaKey.init(rawValue:))
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        script = try c.decodeIfPresent(String.self, forKey: .script) ?? ""
        axis = try c.decodeIfPresent(String.self, forKey: .axis).flatMap(ScrollAxis.init(rawValue:))
        delta = try c.decodeIfPresent(Int.self, forKey: .delta) ?? 0
        ageMs = max(0, try c.decodeIfPresent(Int.self, forKey: .ageMs) ?? 0)
    }
}

public struct ControlCommands: Decodable, Equatable, Sendable {
    public let commands: [ControlCommand]

    enum CodingKeys: String, CodingKey { case commands }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        commands = try c.decodeIfPresent([ControlCommand].self, forKey: .commands) ?? []
    }
}

public enum ThisMac {
    public static func name(source: String, computerName: String) -> String {
        let s = source.trimmingCharacters(in: .whitespaces)
        return MusicPlayerInfo.clip(s.isEmpty ? computerName : s, MusicPlayerInfo.playerNameLimit)
    }
}
