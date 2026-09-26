import Foundation

/// Returns the priority-winning session for the menu/tray, porting
/// render.PickWinning: waiting > error > running > done, then most-recently
/// updated within that state. Idle/unknown states never win. nil if none active.
public func pickWinning(_ sessions: [Session]) -> Session? {
    let order = ["waiting", "error", "running", "done"]
    for state in order {
        let group = sessions.filter { $0.state == state }
        if let best = group.max(by: { $0.updatedAt < $1.updatedAt }) {
            return best
        }
    }
    return nil
}
