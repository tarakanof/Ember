import Foundation

public struct PomodoroItem: Hashable, Sendable, Identifiable {
    public let action: PomodoroAction
    public let title: LocalizedStringResource
    public let systemImage: String
    public let shortcutKey: Character?

    public var id: PomodoroAction { action }

    public static func == (a: PomodoroItem, b: PomodoroItem) -> Bool { a.action == b.action }
    public func hash(into h: inout Hasher) { h.combine(action) }
}

public enum PomodoroControls {
    public static let primaryShortcutKey: Character = "p"

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
