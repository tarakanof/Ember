import Foundation

public struct KnobMood: Equatable, Sendable {
    public var mood: BotMood
    public var host: String
    public var hostColor: RGB?
    public var tool: String

    public init(mood: BotMood, host: String = "", hostColor: RGB? = nil, tool: String = "") {
        self.mood = mood; self.host = host; self.hostColor = hostColor; self.tool = tool
    }

    public init(sessions: [Session], maxChars: Int = 10) {
        guard let win = pickWinning(sessions) else {
            self.init(mood: .idle)
            return
        }
        let peers = sessions.filter { $0.state == win.state && !$0.source.isEmpty }
        var count: [String: Int] = [:]
        for s in peers { count[s.source.uppercased(), default: 0] += 1 }
        let lead = count.min { a, b in a.value != b.value ? a.value > b.value : a.key < b.key }?.key ?? ""
        let mine = peers.filter { $0.source.uppercased() == lead }
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

    static func leadLabel(_ lead: String, hosts: Int, maxChars: Int) -> String {
        let suffix = hosts > 1 ? " +\(hosts - 1)" : ""
        let name = label(lead, maxChars: max(maxChars - suffix.count, 0))
        return name.isEmpty ? "" : name + suffix
    }

    public var showsHost: Bool { !host.isEmpty && (mood == .working || mood == .waiting || mood == .error) }
}
