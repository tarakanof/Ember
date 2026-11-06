import Foundation

public struct KnobPomoFace: Equatable, Sendable {
    public enum Mode: Sendable, Equatable { case idle, running, paused, parked }

    public var mode: Mode
    public var fraction: Double
    public var arc: RGB?
    public var track: RGB
    public var time: String
    public var timeColor: RGB
    public var phase: String
    public var phaseColor: RGB
    public var round: String

    public init(state: PomoState?, fetchedAt: Date?, now: Date, note: String? = nil, theme: KnobTheme.Pomodoro) {
        let c = theme.colors
        guard var s = state else {
            self.init(mode: .idle, fraction: 0, arc: nil, track: c.idleTrack, time: "--:--", timeColor: c.textDim,
                      phase: note ?? "OFFLINE", phaseColor: c.note, round: "")
            return
        }
        let mode = Self.mode(s)
        if mode == .running, let at = fetchedAt {
            s.remainingSec = max(0, s.remainingSec - Int(now.timeIntervalSince(at).rounded(.down)))
        }
        let base: RGB = switch s.phase {
        case "focus": c.focus
        case "short_break", "long_break": c.break
        default: c.other
        }
        let active = mode != .idle
        let arc = mode == .running ? base : base.scaled(theme.pausedGain)
        let blinkOff = mode == .paused && Int(now.timeIntervalSince1970.rounded(.down)) % 2 != 0
        var phase: String, phaseColor = arc
        switch mode {
        case .idle: phase = "PUSH TO START"; phaseColor = c.note
        case .paused: phase = "PAUSED"
        case .parked: phase = "\(Self.phaseName(s.phase)) NEXT"
        case .running: phase = Self.phaseName(s.phase)
        }
        if let note { phase = note; phaseColor = c.note }
        self.init(mode: mode,
                  fraction: active ? Self.fraction(s) : 0,
                  arc: active ? arc : nil,
                  track: active ? base.scaled(theme.trackGain) : c.idleTrack,
                  time: active ? Self.mmss(s.remainingSec) : "--:--",
                  timeColor: !active || blinkOff ? c.textDim : c.text,
                  phase: phase, phaseColor: phaseColor,
                  round: s.round > 0 ? "\(s.round) DONE" : "")
    }

    public init(mode: Mode, fraction: Double, arc: RGB?, track: RGB, time: String, timeColor: RGB,
                phase: String, phaseColor: RGB, round: String) {
        self.mode = mode; self.fraction = fraction; self.arc = arc; self.track = track
        self.time = time; self.timeColor = timeColor; self.phase = phase; self.phaseColor = phaseColor
        self.round = round
    }

    static func mode(_ s: PomoState) -> Mode {
        if s.phase == "idle" || s.phase.isEmpty { return .idle }
        if s.paused { return .paused }
        return s.running ? .running : .parked
    }

    static func fraction(_ s: PomoState) -> Double {
        guard s.plannedSec > 0 else { return 0 }
        return min(max(Double(s.remainingSec) / Double(s.plannedSec), 0), 1)
    }

    static func phaseName(_ phase: String) -> String {
        switch phase {
        case "focus": "FOCUS"
        case "short_break": "BREAK"
        case "long_break": "LONG BREAK"
        default: "TIMER"
        }
    }

    static func mmss(_ seconds: Int) -> String {
        let s = max(seconds, 0)
        return String(format: "%02d:%02d", s / 60, s % 60)
    }
}
