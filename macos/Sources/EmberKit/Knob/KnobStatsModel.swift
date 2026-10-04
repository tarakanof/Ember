import Foundation
import Observation

/// The stats reads the dashboard needs (`KnobService`; a fake in tests).
public protocol KnobStatsService: Sendable {
    func stats(id: String, range: KnobStatsRange) async throws -> KnobStats
    /// Asks the knob for 5 s reports for `seconds` (0 stops); returns the
    /// server's deadline, nil when stopped.
    @discardableResult
    func setLive(id: String, seconds: Int) async throws -> Date?
}

struct KnobLiveAnswer: Decodable {
    let liveUntil: Date?
    enum CodingKeys: String, CodingKey { case liveUntil = "live_until" }
}

extension KnobService: KnobStatsService {
    public func stats(id: String, range: KnobStatsRange) async throws -> KnobStats {
        try await client.get("/v1/devices/\(Self.escape(id))/stats", query: [URLQueryItem(name: "range", value: range.rawValue)])
    }

    @discardableResult
    public func setLive(id: String, seconds: Int) async throws -> Date? {
        let answer: KnobLiveAnswer = try await client.request("POST", "/v1/devices/\(Self.escape(id))/stats/live",
                                                               body: ["seconds": seconds])
        return answer.liveUntil
    }
}

/// The Dashboard's knob section: polls the knob's stats while the window is
/// visible, and keeps the knob in live mode (5 s reports) while the
/// 15-minute range, the only one that shows them, is selected.
@MainActor
@Observable
public final class KnobStatsModel {
    public private(set) var stats: Loadable<KnobStats> = .loading
    public var range: KnobStatsRange = .fifteenMinutes

    /// How long one live request lasts; renewed every `liveRenewal` so it
    /// lapses within this long after the window goes away uncleanly.
    public static let liveSeconds = 180
    public static let liveRenewal: TimeInterval = 60

    @ObservationIgnored private var service: any KnobStatsService
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let now: @Sendable () -> Date
    @ObservationIgnored private var deviceID: String?

    public init(service: any KnobStatsService,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
                now: @escaping @Sendable () -> Date = { Date() }) {
        self.service = service
        self.sleep = sleep
        self.now = now
    }

    /// Points the model at another server.
    public func configure(service next: any KnobStatsService) {
        service = next
        stats = .loading
    }

    /// Fetches the current range once.
    public func refresh(deviceID id: String) async {
        if id != deviceID {
            deviceID = id
            stats = .loading
        }
        let range = range
        do {
            let next = try await service.stats(id: id, range: range)
            guard range == self.range, id == deviceID else { return }
            if stats.value != next || stats.error != nil { stats = .loaded(next, at: now()) }
        } catch is CancellationError {
        } catch {
            stats = stats.afterFailure(FeedError(error))
        }
    }

    /// Polls until the caller's task is cancelled, keeping live mode on
    /// while diagnostics are; on cancel it stops live mode.
    public func run(deviceID id: String) async {
        let service = service
        var liveAt: Date?
        while !Task.isCancelled {
            await refresh(deviceID: id)
            let wantsLive = range == .fifteenMinutes
                && stats.value.map { $0.deviceID == id && $0.diagnostics != .off } == true
            if wantsLive, liveAt.map({ now().timeIntervalSince($0) >= Self.liveRenewal }) ?? true {
                if (try? await service.setLive(id: id, seconds: Self.liveSeconds)) != nil { liveAt = now() }
            } else if !wantsLive, liveAt != nil {
                liveAt = nil
                _ = try? await service.setLive(id: id, seconds: 0)
            }
            do { try await sleep(range.pollInterval) } catch { break }
        }
        if liveAt != nil {
            Task { try? await service.setLive(id: id, seconds: 0) }
        }
    }
}
