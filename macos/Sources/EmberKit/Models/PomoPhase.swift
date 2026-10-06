import Foundation

public enum PomoPhase: Hashable, Sendable {
    case idle, focus, shortBreak, longBreak
    case unknown(String)

    public init(wire: String) {
        switch wire {
        case "idle", "": self = .idle
        case "focus": self = .focus
        case "short_break": self = .shortBreak
        case "long_break": self = .longBreak
        default: self = .unknown(wire)
        }
    }

    public var displayName: LocalizedStringResource {
        switch self {
        case .idle: "Idle"
        case .focus: "Focus"
        case .shortBreak: "Short Break"
        case .longBreak: "Long Break"
        case .unknown(let s):
            "\(s.split(separator: "_").map { $0.prefix(1).uppercased() + $0.dropFirst() }.joined(separator: " "))"
        }
    }

    public var isBreak: Bool { self == .shortBreak || self == .longBreak }
}

extension PomoState {
    public enum Mode: Hashable, Sendable {
        case idle
        case running
        case paused
        case parked
    }

    public var phaseEnum: PomoPhase { PomoPhase(wire: phase) }

    public var mode: Mode {
        if phaseEnum == .idle { return .idle }
        if paused { return .paused }
        if running { return .running }
        return .parked
    }
}
