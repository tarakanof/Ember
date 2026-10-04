import Foundation

/// A byte pipe to one knob: USB serial today, BLE later. `events` finishes
/// when the link drops (unplug, reboot).
public protocol KnobLink: AnyObject, Sendable {
    var events: AsyncStream<KnobEvent> { get }
    func send(_ bytes: [UInt8]) async throws
    func close()
}

/// Opens links to knobs.
public protocol KnobLinkOpener: Sendable {
    /// Opens the port at `path`.
    func open(path: String) async throws -> any KnobLink
    /// Waits up to `timeout` for the knob with this USB serial number to
    /// (re)appear, then opens it.
    func reopen(serialNumber: String, timeout: Duration) async throws -> any KnobLink
}

public enum KnobLinkError: Error, Equatable, Sendable {
    case openFailed(String)
    case writeFailed(String)
    case closed
    case notFound
    /// Another program (idf.py monitor, esptool) has the port.
    case busy
}

/// Waits for events on one link: buffers what arrives so a reply that
/// beats the waiter isn't lost.
public actor KnobSession {
    public nonisolated let link: any KnobLink
    private var queue: [KnobEvent] = []
    private var closed = false
    private var waiters: [Int: Waiter] = [:]
    private var nextWaiter = 0
    private var nextID = 1
    private var reader: Task<Void, Never>?
    /// Every event, for logs observers (the setup sheet shows nothing of it).
    private var observers: [@Sendable (KnobEvent) -> Void] = []

    private struct Waiter {
        let match: @Sendable (KnobEvent) -> Bool
        let continuation: CheckedContinuation<KnobEvent?, Never>
    }

    public init(link: any KnobLink) {
        self.link = link
    }

    /// Starts reading; call once.
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

    /// Sends a `CINDER1` request and returns its id.
    @discardableResult
    public func send(_ request: CinderLineCodec.Request) async throws -> Int {
        let id = nextID
        nextID += 1
        try await send(try CinderLineCodec.encode(request, id: id))
        return id
    }

    /// Sends a `CINDER1` request and waits for the reply with its id.
    public func call(_ request: CinderLineCodec.Request, timeout: Duration) async throws -> CinderLineCodec.Reply {
        let id = try await send(request)
        return try await expect(timeout: timeout) { e in
            if case .cinder(.reply(let r)) = e, r.id == id { return r }
            return nil
        }
    }

    /// The first buffered or future event `match` maps to a value; throws
    /// `.closed` when the link drops first and `KnobTimeout` after `timeout`.
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

    /// Drops everything buffered (stale replies from before a step).
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
