import Foundation

public struct Heatmap: Decodable, Sendable, Equatable {
    public var grid: [[Int]]
    public var calendar: [FocusBucket]
    public var days: Int

    public init(grid: [[Int]], calendar: [FocusBucket], days: Int) {
        self.grid = grid
        self.calendar = calendar
        self.days = days
    }

    public func minutes(weekday: Int, hour: Int) -> Int {
        guard grid.indices.contains(weekday), grid[weekday].indices.contains(hour) else { return 0 }
        return grid[weekday][hour]
    }
}
