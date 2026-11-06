import Foundation

extension Session {
    public enum State: Hashable, Sendable {
        case running, waiting, done, error, idle
        case unknown(String)

        public init(wire: String) {
            switch wire {
            case "running": self = .running
            case "waiting": self = .waiting
            case "done": self = .done
            case "error": self = .error
            case "idle", "": self = .idle
            default: self = .unknown(wire)
            }
        }

        public var wire: String {
            switch self {
            case .running: "running"
            case .waiting: "waiting"
            case .done: "done"
            case .error: "error"
            case .idle: "idle"
            case .unknown(let s): s
            }
        }

        public var displayName: LocalizedStringResource {
            switch self {
            case .running: "Running"
            case .waiting: "Waiting"
            case .done: "Done"
            case .error: "Error"
            case .idle: "Idle"
            case .unknown(let s):
                s.isEmpty ? "Unknown" : "\(s.prefix(1).uppercased() + s.dropFirst())"
            }
        }

        public var sortRank: Int {
            switch self {
            case .waiting: 0
            case .error: 1
            case .running: 2
            case .done: 3
            case .idle: 4
            case .unknown: 5
            }
        }

        public var color: RGB { stateColorRGB(wire) }
    }

    public var stateEnum: State { State(wire: state) }
}
