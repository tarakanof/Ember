import Foundation

public struct KnobMood: Equatable, Sendable {
    public var mood: BotMood
    public var host: String
    public var hostColor: RGB?
    public var tool: String
    public var sub: String
    public var lead: String

    public static let projectSlots = 10
    public static let labelSlots = 12

    public init(mood: BotMood, host: String = "", hostColor: RGB? = nil, tool: String = "", sub: String = "",
                lead: String? = nil) {
        self.mood = mood; self.host = host; self.hostColor = hostColor; self.tool = tool
        self.sub = sub; self.lead = lead ?? host
    }

    public init(sessions: [Session], maxChars: Int = 10) {
        guard let win = pickWinning(sessions) else {
            self.init(mood: .idle)
            return
        }
        let peers = sessions.filter { $0.state == win.state && !$0.source.isEmpty }
        var count: [String: Int] = [:]
        for s in peers { count[s.source.uppercased(), default: 0] += 1 }
        if win.state == "waiting" || win.state == "done" {
            let lead = win.source.uppercased()
            let label = Self.leadLabel(lead, hosts: lead.isEmpty ? 0 : count.count, maxChars: maxChars)
            let same = sessions.filter { $0.state == win.state }.count
            let project = Self.projectLabel(win.project ?? "", others: same - 1)
            self.init(mood: BotMood(state: win.state), host: project.isEmpty ? label : project,
                      hostColor: win.sourceColor.flatMap(RGB.init(hex:)), tool: win.tool,
                      sub: !project.isEmpty && count.count > 1 ? Self.leadLabel(lead, hosts: 1, maxChars: maxChars) : "",
                      lead: label)
            return
        }
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

    public static func projectLabel(_ project: String, others: Int) -> String {
        projectLabel(bytes: Array(project.utf8), others: others)
    }

    static func projectLabel(bytes: [UInt8], others: Int) -> String {
        var name: [UInt8] = []
        var word = false, more = false
        for b in bytes where b >= 0x20 && b <= 0x7E && !(name.isEmpty && b == 0x20) {
            word = word || (b >= 0x30 && b <= 0x39) || (b >= 0x41 && b <= 0x5A) || (b >= 0x61 && b <= 0x7A)
            if name.count < projectSlots { name.append(b) } else if b != 0x20 { more = true }
        }
        while name.last == 0x20 { name.removeLast() }
        guard word else { return "" }
        let suffix = others > 0 ? " +\(min(others, 99))" : ""
        let room = min(labelSlots - suffix.count, projectSlots)
        var cut = ""
        if name.count > room || more {
            name = Array(name.prefix(room - 1))
            while name.last == 0x20 { name.removeLast() }
            cut = "..."
        }
        return String(decoding: name, as: UTF8.self) + cut + suffix
    }

    public var showsHost: Bool {
        !host.isEmpty && (mood == .working || mood == .waiting || mood == .error || mood == .done)
    }

    public var shownSub: String { mood == .waiting || mood == .done ? sub : "" }
}
