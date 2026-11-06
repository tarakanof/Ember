import Foundation

public protocol KnobLink: AnyObject, Sendable {
    var events: AsyncStream<KnobEvent> { get }
    func send(_ bytes: [UInt8]) async throws
    func close()
}

public protocol KnobLinkOpener: Sendable {
    func open(path: String) async throws -> any KnobLink
    func reopen(serialNumber: String, timeout: Duration) async throws -> any KnobLink
}

public enum KnobLinkError: Error, Equatable, Sendable {
    case openFailed(String)
    case writeFailed(String)
    case closed
    case notFound
    case busy
}

public actor KnobSession {
    public nonisolated let link: any KnobLink
    private var queue: [KnobEvent] = []
    private var closed = false
    private var waiters: [Int: Waiter] = [:]
    private var nextWaiter = 0
    private var nextID = 1
    private var reader: Task<Void, Never>?
    private var observers: [@Sendable (KnobEvent) -> Void] = []

    private struct Waiter {
        let match: @Sendable (KnobEvent) -> Bool
        let continuation: CheckedContinuation<KnobEvent?, Never>
    }

    public init(link: any KnobLink) {
        self.link = link
    }

    public func start() {
        guard reader == nil else { return }
        let events = link.events
        reader = Task { [weak self] in
            for await e in events { await self?.receive(e) }
            await self?.finish()
        }
    }

    public func observe(_ f: @escaping @Sendable (KnobEvent) -> Void) { observers.append(f) }

    public var isClosed: Bool { closed }

    public func send(_ bytes: [UInt8]) async throws {
        guard !closed else { throw KnobLinkError.closed }
        try await link.send(bytes)
    }

    @discardableResult
    public func send(_ request: CinderLineCodec.Request) async throws -> Int {
        let id = nextID
        nextID += 1
        try await send(try CinderLineCodec.encode(request, id: id))
        return id
    }

    public func call(_ request: CinderLineCodec.Request, timeout: Duration) async throws -> CinderLineCodec.Reply {
        let id = try await send(request)
        return try await expect(timeout: timeout) { e in
            if case .cinder(.reply(let r)) = e, r.id == id { return r }
            return nil
        }
    }

    /// Throws `.closed` if the link drops first, `KnobTimeout` after `timeout`.
    public func expect<T: Sendable>(timeout: Duration,
                                    _ match: @escaping @Sendable (KnobEvent) -> T?) async throws -> T {
        if let i = queue.firstIndex(where: { match($0) != nil }) {
            let e = queue.remove(at: i)
            return match(e)!
        }
        guard !closed else { throw KnobLinkError.closed }
        try Task.checkCancellation()
        let key = nextWaiter
        nextWaiter += 1
        let timer = Task { [weak self] in
            try? await Task.sleep(for: timeout)
            await self?.expire(key)
        }
        let event = await withTaskCancellationHandler {
            await withCheckedContinuation { (c: CheckedContinuation<KnobEvent?, Never>) in
                waiters[key] = Waiter(match: { match($0) != nil }, continuation: c)
            }
        } onCancel: {
            Task { [weak self] in await self?.expire(key) }
        }
        timer.cancel()
        guard let event else {
            if Task.isCancelled { throw CancellationError() }
            throw closed ? KnobLinkError.closed : KnobTimeout()
        }
        return match(event)!
    }

    public func drain() { queue.removeAll() }

    public func close() {
        link.close()
        finish()
    }

    private func receive(_ e: KnobEvent) {
        for o in observers { o(e) }
        if let (key, w) = waiters.first(where: { $0.value.match(e) }) {
            waiters[key] = nil
            w.continuation.resume(returning: e)
            return
        }
        guard case .log = e else {
            queue.append(e)
            if queue.count > 64 { queue.removeFirst() }
            return
        }
    }

    private func expire(_ key: Int) {
        waiters.removeValue(forKey: key)?.continuation.resume(returning: nil)
    }

    private func finish() {
        guard !closed else { return }
        closed = true
        reader?.cancel()
        for w in waiters.values { w.continuation.resume(returning: nil) }
        waiters.removeAll()
    }
}

public struct KnobTimeout: Error, Equatable, Sendable {}
