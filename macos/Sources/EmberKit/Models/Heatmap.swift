import Foundation

/// GET /v1/pomodoro/heatmap — when focus happens, as a weekday × hour grid
/// plus a per-day calendar for the consistency strip.
public struct Heatmap: Decodable, Sendable, Equatable {
    /// Completed-focus minutes, `grid[weekday][hour]`, weekday 0 = Sunday
    /// (server-local time). Always 7 × 24.
    public var grid: [[Int]]
    /// Per logical day, oldest first. Days without focus are absent.
    public var calendar: [FocusBucket]
    /// The window the server covered.
    public var days: Int

    public init(grid: [[Int]], calendar: [FocusBucket], days: Int) {
        self.grid = grid
        self.calendar = calendar
        self.days = days
    }

    /// Minutes for a weekday (0 = Sunday) and hour; 0 outside the grid.
    public func minutes(weekday: Int, hour: Int) -> Int {
        guard grid.indices.contains(weekday), grid[weekday].indices.contains(hour) else { return 0 }
        return grid[weekday][hour]
    }
}
