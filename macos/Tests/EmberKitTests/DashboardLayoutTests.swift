import Testing
import Foundation
@testable import EmberKit

@Test(arguments: [(1200.0, 3), (1040, 3), (1039, 2), (720, 2), (700, 2), (699, 1), (320, 1)])
func dashboardColumnsFollowWidth(width: Double, columns: Int) {
    #expect(DashboardLayout.columns(forWidth: width) == columns)
}

/// The §2.3 card list: W = wide, S = standard.
private let specCards: [(id: String, size: CardSize)] = [
    ("clock", .wide), ("focus", .standard), ("usage", .standard), ("upcoming", .standard),
    ("agents", .wide), ("last7", .standard), ("weeks", .standard), ("workhours", .wide),
    ("heatmap", .wide), ("agenttime", .standard), ("health", .standard), ("weather", .standard),
]

@Test func twoColumnRunsMatchTheWireframe() {
    let runs = DashboardLayout.runs(specCards, columns: 2)
    #expect(runs == [
        .wide("clock"), .grid(["focus", "usage", "upcoming", "last7"]), .wide("agents"),
        .grid(["weeks", "agenttime"]), .wide("workhours"), .wide("heatmap"),
        .grid(["health", "weather"]),
    ])
}

@Test func threeColumnRunsFillRowsBeforeAWideCard() {
    let runs = DashboardLayout.runs(specCards, columns: 3)
    #expect(runs == [
        .wide("clock"), .grid(["focus", "usage", "upcoming"]), .wide("agents"),
        .grid(["last7", "weeks", "agenttime"]), .wide("workhours"), .wide("heatmap"),
        .grid(["health", "weather"]),
    ])
}

@Test func oneColumnKeepsTheListOrder() {
    let runs = DashboardLayout.runs(specCards, columns: 1)
    let flat = runs.flatMap { run -> [String] in
        switch run {
        case .wide(let id): [id]
        case .grid(let ids): ids
        }
    }
    #expect(flat == specCards.map(\.id))
}

@Test func hiddenCardsReflowAndTrailingWideCardsAreKept() {
    // Usage, Upcoming and Weather hidden; a wide card waiting at the end still renders.
    let cards: [(id: String, size: CardSize)] = [
        ("clock", .wide), ("focus", .standard), ("agents", .wide), ("heatmap", .wide),
    ]
    #expect(DashboardLayout.runs(cards, columns: 2) == [
        .wide("clock"), .grid(["focus"]), .wide("agents"), .wide("heatmap"),
    ])
    #expect(DashboardLayout.runs([(id: "a", size: CardSize.standard)], columns: 0) == [.grid(["a"])])
}
