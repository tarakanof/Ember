import Foundation
@testable import EmberKit

@MainActor
final class ManualClock {
    private(set) var now: Duration = .zero
    private var sleepers: [(id: Int, deadline: Duration, cont: CheckedContinuation<Void, Error>)] = []
    private var cancelledEarly: Set<Int> = []
    private var nextID = 0

    var sleeperCount: Int { sleepers.count }

    func sleep(_ d: Duration) async throws {
        nextID += 1
        let id = nextID
        let deadline = now + d
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Void, Error>) in
                if Task.isCancelled || cancelledEarly.remove(id) != nil {
                    cont.resume(throwing: CancellationError())
                } else {
                    sleepers.append((id, deadline, cont))
                }
            }
        } onCancel: {
            Task { @MainActor in self.cancel(id) }
        }
    }

    private func cancel(_ id: Int) {
        guard let i = sleepers.firstIndex(where: { $0.id == id }) else {
            cancelledEarly.insert(id)
            return
        }
        sleepers.remove(at: i).cont.resume(throwing: CancellationError())
    }

    func advance(by d: Duration) async {
        let target = now + d
        await settle()
        while let next = sleepers.filter({ $0.deadline <= target }).min(by: { $0.deadline < $1.deadline }) {
            now = next.deadline
            sleepers.removeAll { $0.id == next.id }
            next.cont.resume()
            await settle()
        }
        now = target
        await settle()
    }

    func settle() async {
        for _ in 0..<200 { await Task.yield() }
    }

    var sleepFn: RefreshCoordinator.Sleep { { [self] d in try await self.sleep(d) } }
    var nowFn: RefreshCoordinator.Now { { [self] in self.now } }
}
