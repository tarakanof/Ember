import Foundation

/// A Pomodoro control: `POST /v1/pomodoro/<rawValue>` (see `ActionRunner`).
public enum PomodoroAction: String, CaseIterable, Sendable {
    case start, pause, resume, stop, skip
}
