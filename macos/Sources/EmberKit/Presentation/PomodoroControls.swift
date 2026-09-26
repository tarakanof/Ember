import Foundation

/// One Pomodoro control, as the menu, the Dashboard toolbar and the Dock menu
/// all show it.
public struct PomodoroItem: Hashable, Sendable, Identifiable {
    public let action: PomodoroAction
    /// Title-case menu title ("Start Focus").
    public let title: String
    /// SF Symbol.
    public let systemImage: String
    /// The key for ⇧⌘-key, on the one context-sensitive primary item
    /// (Start / Pause / Resume). nil on the others.
    public let shortcutKey: Character?

    public var id: PomodoroAction { action }
}

/// The single source of which Pomodoro controls apply to a timer state.
public enum PomodoroControls {
    /// The key of the primary control's ⇧⌘ shortcut.
    public static let primaryShortcutKey: Character = "p"

    /// Idle: Start. Running: Pause, Skip, Stop. Paused: Resume, Skip, Stop.
    /// Parked (between phases, not paused): Resume, Stop. nil (feature off or
    /// not loaded) is treated as idle.
    public static func items(for state: PomoState?) -> [PomodoroItem] {
        switch state?.mode ?? .idle {
        case .idle: [start]
        case .running: [pause, skip, stop]
        case .paused: [resume, skip, stop]
        case .parked: [resume, stop]
        }
    }

    static let start = PomodoroItem(action: .start, title: "Start Focus", systemImage: "play.fill",
                                    shortcutKey: primaryShortcutKey)
    static let pause = PomodoroItem(action: .pause, title: "Pause", systemImage: "pause.fill",
                                    shortcutKey: primaryShortcutKey)
    static let resume = PomodoroItem(action: .resume, title: "Resume", systemImage: "play.fill",
                                     shortcutKey: primaryShortcutKey)
    static let skip = PomodoroItem(action: .skip, title: "Skip Phase", systemImage: "forward.end.fill",
                                   shortcutKey: nil)
    static let stop = PomodoroItem(action: .stop, title: "Stop", systemImage: "stop.fill",
                                   shortcutKey: nil)
}
