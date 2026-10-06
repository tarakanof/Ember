import Foundation

public enum CardSize: Hashable, Sendable {
    case standard
    case wide
}

public enum DashboardLayout {
    public enum Run<ID: Hashable & Sendable>: Hashable, Sendable {
        case wide(ID)
        case grid([ID])
    }

    public static func columns(forWidth width: Double) -> Int {
        if width >= 1040 { return 3 }
        if width >= 700 { return 2 }
        return 1
    }

    public static func runs<ID: Hashable & Sendable>(_ cards: [(id: ID, size: CardSize)], columns: Int) -> [Run<ID>] {
        let cols = max(1, columns)
        var out: [Run<ID>] = []
        var pending: [ID] = []
        var waiting: [ID] = []
        func flush() {
            if !pending.isEmpty { out.append(.grid(pending)); pending = [] }
            out += waiting.map { .wide($0) }
            waiting = []
        }
        for card in cards {
            switch card.size {
            case .standard:
                pending.append(card.id)
                if pending.count % cols == 0, !waiting.isEmpty { flush() }
            case .wide:
                if pending.count % cols == 0, waiting.isEmpty {
                    flush()
                    out.append(.wide(card.id))
                } else {
                    waiting.append(card.id)
                }
            }
        }
        flush()
        return out
    }
}
