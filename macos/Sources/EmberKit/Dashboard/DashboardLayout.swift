import Foundation

/// How much of a Dashboard row a card takes.
public enum CardSize: Hashable, Sendable {
    /// One grid cell.
    case standard
    /// A full row of its own.
    case wide
}

/// The Dashboard grid: column count from the width, and the ordered card list
/// cut into rows (design §2.2).
public enum DashboardLayout {
    /// One run of the grid: a wide card alone, or consecutive standard cards
    /// laid out in `columns` columns.
    public enum Run<ID: Hashable & Sendable>: Hashable, Sendable {
        case wide(ID)
        case grid([ID])
    }

    /// 3 columns from 1040 pt, 2 from 700 pt, else 1.
    public static func columns(forWidth width: Double) -> Int {
        if width >= 1040 { return 3 }
        if width >= 700 { return 2 }
        return 1
    }

    /// Cuts `cards` into runs for a grid of `columns` columns. Standard cards
    /// keep their order and share grid runs; a wide card is its own run. A
    /// wide card that would leave a hole in a half-filled row waits until the
    /// standard cards after it have filled that row (the §2.4 wireframe:
    /// Upcoming pairs with Last 7 days before Agents).
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
