import Foundation
import OSLog

public protocol NowPlayingCommandSource: Sendable {
    func commands(player: String, wait: Int) async throws -> [NowPlayingCommand]
}

@MainActor
public final class MusicCommandListener {
    public static let wait = 25
    private static let log = Logger(subsystem: "com.ember.Ember", category: "music")
    static let firstBackoff: Duration = .seconds(5)
    static let maxBackoff: Duration = .seconds(60)
    static let missingRouteBackoff: Duration = .seconds(300)
    static let minRound: Duration = .seconds(1)
    static let maxAge: Duration = .seconds(5)

    public typealias Sleep = @Sendable (Duration) async throws -> Void

    private let bridge: MusicBridge
    private let pusher: AppleMusicPusher
    private let sleep: Sleep
    private var task: Task<Void, Never>?
    private var loggedDenied = false

    public init(bridge: MusicBridge, pusher: AppleMusicPusher,
                sleep: @escaping Sleep = { try await Task.sleep(for: $0) }) {
        self.bridge = bridge
        self.pusher = pusher
        self.sleep = sleep
    }

    public var isRunning: Bool { task != nil }

    public func start(source: NowPlayingCommandSource, player: String) {
        stop()
        let player = MusicPlayerInfo.clip(player, 64)
        task = Task { [weak self] in await self?.run(source: source, player: player) }
    }

    public func stop() {
        task?.cancel()
        task = nil
    }

    public func join() async { await task?.value }

    private func run(source: NowPlayingCommandSource, player: String) async {
        var backoff = Self.firstBackoff
        let clock = ContinuousClock()
        while !Task.isCancelled {
            let began = clock.now
            do {
                let commands = try await source.commands(player: player, wait: Self.wait)
                backoff = Self.firstBackoff
                await execute(commands, received: clock.now)
                if commands.isEmpty, clock.now - began < Self.minRound { try await sleep(Self.minRound) }
            } catch {
                if Task.isCancelled || error is CancellationError { return }
                let pause: Duration
                switch error as? APIError {
                case .http(404, _)?: pause = Self.missingRouteBackoff
                case .rateLimited(let after)?: pause = max(after, Self.firstBackoff)
                default:
                    pause = backoff
                    backoff = min(backoff * 2, Self.maxBackoff)
                }
                do { try await sleep(pause) } catch { return }
            }
        }
    }

    func execute(_ commands: [NowPlayingCommand], received: ContinuousClock.Instant = .now) async {
        var ran = false
        for c in commands where c.action != nil {
            let age = Duration.milliseconds(c.ageMs) + (ContinuousClock.now - received)
            if age > Self.maxAge {
                Self.log.notice("dropped a stale knob command (\(c.action?.rawValue ?? "", privacy: .public))")
                continue
            }
            guard await bridge.isRunning() else { continue }
            guard await bridge.canControl() else {
                if !loggedDenied { Self.log.notice("knob command skipped: Automation for Music not granted") }
                loggedDenied = true
                continue
            }
            loggedDenied = false
            if await bridge.perform(c) { ran = true }
        }
        guard ran, let info = await bridge.snapshot() else { return }
        pusher.submit(info)
    }
}
