import Foundation

/// Where playback commands for this Mac come from: the server's long-poll.
public protocol NowPlayingCommandSource: Sendable {
    /// Waits up to `wait` seconds for commands addressed to `player`.
    func commands(player: String, wait: Int) async throws -> [NowPlayingCommand]
}

/// Runs the knob's playback commands (Ember #280) on Music.app while the
/// pusher is on: long-polls the server, performs each command only while
/// Music runs (never launching it), then re-reports the player so the knob
/// sees the result (Music posts no notification for a volume change).
@MainActor
public final class MusicCommandListener {
    public static let wait = 25
    static let firstBackoff: Duration = .seconds(5)
    static let maxBackoff: Duration = .seconds(60)
    /// An older server without the route: ask again much later.
    static let missingRouteBackoff: Duration = .seconds(300)
    /// A long-poll that answers empty at once (a misbehaving proxy) must not spin.
    static let minRound: Duration = .seconds(1)

    public typealias Sleep = @Sendable (Duration) async throws -> Void

    private let bridge: MusicBridge
    private let pusher: AppleMusicPusher
    private let sleep: Sleep
    private var task: Task<Void, Never>?

    public init(bridge: MusicBridge, pusher: AppleMusicPusher,
                sleep: @escaping Sleep = { try await Task.sleep(for: $0) }) {
        self.bridge = bridge
        self.pusher = pusher
        self.sleep = sleep
    }

    public var isRunning: Bool { task != nil }

    /// Starts listening as `player` (restarts when already running).
    public func start(source: NowPlayingCommandSource, player: String) {
        stop()
        let player = MusicPlayerInfo.clip(player, 64)   // the name the reports carry
        task = Task { [weak self] in await self?.run(source: source, player: player) }
    }

    public func stop() {
        task?.cancel()
        task = nil
    }

    /// Waits for the listener to end (tests; after `stop`).
    public func join() async { await task?.value }

    private func run(source: NowPlayingCommandSource, player: String) async {
        var backoff = Self.firstBackoff
        let clock = ContinuousClock()
        while !Task.isCancelled {
            let began = clock.now
            do {
                let commands = try await source.commands(player: player, wait: Self.wait)
                backoff = Self.firstBackoff
                await execute(commands)
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

    /// Runs commands in order; one re-report after any that ran.
    func execute(_ commands: [NowPlayingCommand]) async {
        var ran = false
        for c in commands where c.action != nil {
            guard await bridge.isRunning() else { continue }
            if await bridge.perform(c) { ran = true }
        }
        guard ran, let info = await bridge.snapshot() else { return }
        pusher.submit(info)
    }
}
