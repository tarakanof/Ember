import Foundation
import Observation

public protocol ClockStatsService: Sendable {
    func clockStats(range: HardwareRange) async throws -> ClockStats
}

public struct ClockStatsClient: ClockStatsService {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func clockStats(range: HardwareRange) async throws -> ClockStats {
        try await client.get("/v1/clock/stats", query: [URLQueryItem(name: "range", value: range.rawValue)])
    }
}

extension HardwareRange {
    public var clockPollInterval: Duration {
        switch self {
        case .fifteenMinutes: .seconds(15)
        case .hour: .seconds(30)
        case .day: .seconds(60)
        }
    }
}

@MainActor
@Observable
public final class ClockStatsModel {
    public private(set) var stats: Loadable<ClockStats> = .loading
    public var range: HardwareRange = .hour

    @ObservationIgnored private var service: any ClockStatsService
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let now: @Sendable () -> Date

    public init(service: any ClockStatsService,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
                now: @escaping @Sendable () -> Date = { Date() }) {
        self.service = service
        self.sleep = sleep
        self.now = now
    }

    public func configure(service next: any ClockStatsService) {
        service = next
        stats = .loading
    }

    public func refresh() async {
        let range = range
        do {
            let next = try await service.clockStats(range: range)
            guard range == self.range else { return }
            if stats.value != next || stats.error != nil { stats = .loaded(next, at: now()) }
        } catch is CancellationError {
        } catch {
            stats = stats.afterFailure(FeedError(error))
        }
    }

    public func run() async {
        while !Task.isCancelled {
            await refresh()
            do { try await sleep(range.clockPollInterval) } catch { break }
        }
    }
}
