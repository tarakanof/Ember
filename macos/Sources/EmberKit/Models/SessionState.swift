import Foundation

extension Session {
    /// A session's state as a type instead of the wire string, so views never
    /// print "running" or compare strings.
    public enum State: Hashable, Sendable {
        case running, waiting, done, error, idle
        /// A state this app doesn't know yet, kept verbatim.
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

        /// The wire string ("running").
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

        /// Title-case name for the UI ("Running").
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

        /// Sort order for session lists, lowest first. Matches `pickWinning`:
        /// what needs attention comes first.
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

        /// The state colour shared by the menu-bar icon, the bot and badges.
        public var color: RGB { stateColorRGB(wire) }
    }

    /// `state` as a `Session.State`.
    public var stateEnum: State { State(wire: state) }
}
