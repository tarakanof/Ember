import Foundation

public struct KnobShownFollow: Equatable, Sendable {
    struct Seen: Equatable, Sendable {
        var page: String
        var face: String?
        var changedAt: Date
    }

    public private(set) var face: KnobWeatherFaceID?
    public private(set) var current: String?
    private var seen: [String: Seen] = [:]

    public init() {}

    public mutating func follow(_ devices: [KnobDevice]) {
        var next: [String: Seen] = [:]
        var live: [String: Seen] = [:]
        for d in devices where d.kind == KnobDevice.knobKind {
            guard let s = d.shown else {
                next[d.id] = seen[d.id]
                continue
            }
            if let old = seen[d.id], old.page == s.page, old.face == s.face {
                next[d.id] = old
            } else {
                next[d.id] = Seen(page: s.page, face: s.face, changedAt: s.at)
            }
            live[d.id] = next[d.id]
        }
        seen = next
        let keep = current
        let best = live.max { a, b in
            if a.value.changedAt != b.value.changedAt { return a.value.changedAt < b.value.changedAt }
            if a.key == keep || b.key == keep { return b.key == keep }
            return a.key > b.key
        }
        current = best?.key
        guard let s = best?.value else {
            face = nil
            return
        }
        if s.page == "weather", let f = s.face.flatMap(KnobWeatherFaceID.init(rawValue:)) { face = f }
    }

    public mutating func forget() { self = KnobShownFollow() }

    @MainActor
    public static func reported(by feed: KnobShownFeed?, seed devices: [KnobDevice]) -> KnobWeatherFaceID? {
        if let feed { return feed.face }
        return seeded(devices)
    }

    public static func seeded(_ devices: [KnobDevice]) -> KnobWeatherFaceID? {
        var f = KnobShownFollow()
        f.follow(devices)
        return f.face
    }
}

@MainActor
@Observable
public final class KnobShownFeed {
    public static let interval: Duration = .seconds(2)
    public static let maxInterval: Duration = .seconds(30)
    public static let keepAcrossFailures: TimeInterval = 50

    public private(set) var face: KnobWeatherFaceID?

    @ObservationIgnored private var follow = KnobShownFollow()
    @ObservationIgnored private var lastRead: Date?
    @ObservationIgnored public private(set) var failures = 0
    @ObservationIgnored private var task: Task<Void, Never>?
    @ObservationIgnored private(set) var refreshing = false
    @ObservationIgnored private let fetch: @Sendable () async throws -> [KnobDevice]
    @ObservationIgnored private let onDevices: @MainActor ([KnobDevice]) -> Void
    @ObservationIgnored private let now: @Sendable () -> Date
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void

    public init(seed: [KnobDevice] = [], fetch: @escaping @Sendable () async throws -> [KnobDevice],
                onDevices: @escaping @MainActor ([KnobDevice]) -> Void = { _ in },
                now: @escaping @Sendable () -> Date = Date.init,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.fetch = fetch; self.onDevices = onDevices; self.now = now; self.sleep = sleep
        follow.follow(seed)
        face = follow.face
    }

    public convenience init(client: APIClient, seed: [KnobDevice] = [],
                            onDevices: @escaping @MainActor ([KnobDevice]) -> Void = { _ in }) {
        let service = KnobService(client: client)
        self.init(seed: seed, fetch: { try await service.devices() }, onDevices: onDevices)
    }

    public var isPolling: Bool { task != nil }

    public var delay: Duration {
        guard failures > 0 else { return Self.interval }
        return min(Self.interval * (1 << min(failures, 5)), Self.maxInterval)
    }

    public func setVisible(_ visible: Bool) {
        guard visible else {
            task?.cancel()
            task = nil
            return
        }
        guard task == nil else { return }
        let sleep = self.sleep
        task = Task { [weak self] in
            while !Task.isCancelled {
                guard let wait = await self?.poll() else { return }
                try? await sleep(wait)
            }
        }
    }

    private func poll() async -> Duration {
        await refresh()
        return delay
    }

    public func refresh() async {
        refreshing = true
        defer { refreshing = false }
        do {
            let devices = try await fetch()
            guard !Task.isCancelled else { return }
            lastRead = now()
            failures = 0
            onDevices(devices)
            follow.follow(devices)
        } catch {
            guard !Task.isCancelled, !(error is CancellationError) else { return }
            failures += 1
            let kept = lastRead.map { now().timeIntervalSince($0) <= Self.keepAcrossFailures } ?? false
            if !kept { follow.forget() }
        }
        if face != follow.face { face = follow.face }
    }
}
