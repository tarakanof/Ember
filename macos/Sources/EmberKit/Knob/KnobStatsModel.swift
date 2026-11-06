import Foundation
import Observation

public protocol KnobStatsService: Sendable {
    func stats(id: String, range: KnobStatsRange) async throws -> KnobStats
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

@MainActor
@Observable
public final class KnobStatsModel {
    public private(set) var stats: Loadable<KnobStats> = .loading
    public var range: KnobStatsRange = .fifteenMinutes

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

    public func configure(service next: any KnobStatsService) {
        service = next
        stats = .loading
    }

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
