import Foundation

public enum PomodoroAction: String, CaseIterable, Sendable {
    case start, pause, resume, stop, skip
}
