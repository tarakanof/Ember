import Foundation

public struct KnobShownFollow: Equatable, Sendable {
    public private(set) var face: KnobWeatherFaceID?

    public init() {}

    public static func newest(_ devices: [KnobDevice]) -> KnobShown? {
        devices.filter { $0.kind == KnobDevice.knobKind }.compactMap(\.shown).max { $0.at < $1.at }
    }

    public mutating func follow(_ shown: KnobShown?) {
        guard let shown else {
            face = nil
            return
        }
        if shown.page == "weather", let f = shown.face.flatMap(KnobWeatherFaceID.init(rawValue:)) { face = f }
    }
}

@MainActor
@Observable
public final class KnobShownFeed {
    public static let interval: Duration = .seconds(2)
    public static let forgetAfter: TimeInterval = 50

    public private(set) var face: KnobWeatherFaceID?

    @ObservationIgnored private var follow = KnobShownFollow()
    @ObservationIgnored private var lastRead: Date?
    @ObservationIgnored private let fetch: @Sendable () async throws -> [KnobDevice]
    @ObservationIgnored private let now: @Sendable () -> Date
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void

    public init(fetch: @escaping @Sendable () async throws -> [KnobDevice], now: @escaping @Sendable () -> Date = Date.init,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.fetch = fetch; self.now = now; self.sleep = sleep
    }

    public convenience init(client: APIClient) {
        let service = KnobService(client: client)
        self.init(fetch: { try await service.devices() })
    }

    public func run() async {
        while !Task.isCancelled {
            await refresh()
            try? await sleep(Self.interval)
        }
    }

    public func refresh() async {
        do {
            let devices = try await fetch()
            guard !Task.isCancelled else { return }
            lastRead = now()
            follow.follow(KnobShownFollow.newest(devices))
        } catch {
            guard !Task.isCancelled, !(error is CancellationError) else { return }
            if let at = lastRead, now().timeIntervalSince(at) <= Self.forgetAfter { return }
            follow.follow(nil)
        }
        if face != follow.face { face = follow.face }
    }
}
