import Foundation

/// What the knob's bot shows for a set of sessions: the mood from Ember's
/// render priority and the host label, as cinder's `ember_client.c` and
/// `ember_host.c` derive them.
public struct KnobMood: Equatable, Sendable {
    public var mood: BotMood
    /// Uppercased printable ASCII, at most `maxChars`; "" when no single host wins.
    public var host: String

    public init(mood: BotMood, host: String = "") {
        self.mood = mood; self.host = host
    }

    /// The mood and host for `sessions`: waiting > error > running > done, and
    /// the winner's source unless sessions in the winning state disagree.
    public init(sessions: [Session], maxChars: Int = 10) {
        guard let win = pickWinning(sessions) else {
            self.init(mood: .idle)
            return
        }
        let peers = sessions.filter { $0.state == win.state }
        let source = peers.allSatisfy { $0.source == win.source } ? win.source : ""
        self.init(mood: BotMood(state: win.state), host: Self.label(source, maxChars: maxChars))
    }

    static func label(_ s: String, maxChars: Int) -> String {
        let kept = s.unicodeScalars.filter { $0.value >= 0x20 && $0.value <= 0x7E }
        return String(String.UnicodeScalarView(kept).prefix(maxChars)).uppercased()
    }

    /// Whether the knob prints the host under the face (waiting and error only).
    public var showsHost: Bool { !host.isEmpty && (mood == .waiting || mood == .error) }
}
