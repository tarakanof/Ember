import Foundation

public func pickWinning(_ sessions: [Session]) -> Session? {
    let order = ["waiting", "error", "running", "done"]
    for state in order {
        let group = sessions.filter { $0.state == state }
        if let best = group.max(by: enteredEarlier) {
            return best
        }
    }
    return nil
}

func enteredEarlier(_ a: Session, _ b: Session) -> Bool {
    if let sa = a.stateSince, let sb = b.stateSince, sa != sb {
        return sa < sb
    }
    return a.updatedAt < b.updatedAt
}
