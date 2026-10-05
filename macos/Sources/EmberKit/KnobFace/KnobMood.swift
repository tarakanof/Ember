import Foundation

/// What the knob's bot shows for a set of sessions: the mood from Ember's
/// render priority and the host label, as cinder's `ember_client.c` and
/// `ember_host.c` derive them from the server's knob view (`knob_lead.go`).
public struct KnobMood: Equatable, Sendable {
    public var mood: BotMood
    /// Uppercased printable ASCII, at most `maxChars` with the " +N" suffix;
    /// "" when no session in the winning state names a host.
    public var host: String
    /// The lead host's `source_color`; nil draws the label in the mood colour.
    public var hostColor: RGB?
    /// The lead host's tool ("claude", "codex", "t3") for the glyph before the
    /// label; "" draws none.
    public var tool: String

    public init(mood: BotMood, host: String = "", hostColor: RGB? = nil, tool: String = "") {
        self.mood = mood; self.host = host; self.hostColor = hostColor; self.tool = tool
    }

    /// The mood and host for `sessions`: waiting > error > running > done; the
    /// lead is the source with the most sessions in the winning state (ties to
    /// the smaller name), plus " +N" for the other hosts in that state.
    public init(sessions: [Session], maxChars: Int = 10) {
        guard let win = pickWinning(sessions) else {
            self.init(mood: .idle)
            return
        }
        let peers = sessions.filter { $0.state == win.state && !$0.source.isEmpty }
        var count: [String: Int] = [:]
        for s in peers { count[s.source, default: 0] += 1 }
        let lead = count.min { a, b in a.value != b.value ? a.value > b.value : a.key < b.key }?.key ?? ""
        let mine = peers.filter { $0.source == lead }
        let color = mine.lazy.compactMap { $0.sourceColor.flatMap(RGB.init(hex:)) }.first
        let tools = Set(mine.map(\.tool))
        self.init(mood: BotMood(state: win.state),
                  host: Self.leadLabel(lead, hosts: count.count, maxChars: maxChars),
                  hostColor: lead.isEmpty ? nil : color,
                  tool: tools.count == 1 ? tools.first! : "")
    }

    static func label(_ s: String, maxChars: Int) -> String {
        let kept = s.unicodeScalars.filter { $0.value >= 0x20 && $0.value <= 0x7E }
        return String(String.UnicodeScalarView(kept).prefix(maxChars)).uppercased()
    }

    /// cinder's `ember_host_lead_label`: the lead cut so " +N" still fits.
    static func leadLabel(_ lead: String, hosts: Int, maxChars: Int) -> String {
        let suffix = hosts > 1 ? " +\(hosts - 1)" : ""
        let name = label(lead, maxChars: max(maxChars - suffix.count, 0))
        return name.isEmpty ? "" : name + suffix
    }

    /// Whether the knob prints the host along the bottom (working, waiting, error).
    public var showsHost: Bool { !host.isEmpty && (mood == .working || mood == .waiting || mood == .error) }
}
