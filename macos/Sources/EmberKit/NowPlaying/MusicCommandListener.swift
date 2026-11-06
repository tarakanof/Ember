import Foundation
import OSLog

public protocol NowPlayingCommandSource: Sendable {
    func commands(player: String, wait: Int, watched: Bool) async throws -> NowPlayingCommands
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
    static let skipPoll: Duration = .milliseconds(200)
    static let skipPolls = 10

    public typealias Sleep = @Sendable (Duration) async throws -> Void

    private let bridge: MusicBridge
    private let pusher: AppleMusicPusher
    private let sleep: Sleep
    private var task: Task<Void, Never>?
    private var stopped: Task<Void, Never>?
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
        let player = MusicPlayerInfo.clip(player, MusicPlayerInfo.playerNameLimit)
        task = Task { [weak self] in await self?.run(source: source, player: player) }
    }

    public func stop() {
        task?.cancel()
        if let task { stopped = task }
        task = nil
    }

    public func join() async { await (task ?? stopped)?.value }

    private func run(source: NowPlayingCommandSource, player: String) async {
        var backoff = Self.firstBackoff
        var recoverAt: ContinuousClock.Instant?
        let clock = ContinuousClock()
        while !Task.isCancelled {
            let began = clock.now
            do {
                let answer = try await source.commands(player: player, wait: Self.wait, watched: pusher.watched)
                let received = clock.now
                let commands = answer.commands
                pusher.setWatched(answer.watched ?? true)
                backoff = Self.firstBackoff
                if let at = recoverAt, clock.now >= at {
                    let error = await pusher.pushSnapshot()
                    recoverAt = Self.retry(after: error).map { clock.now + $0 }
                }
                await execute(commands, received: received)
                if commands.isEmpty, clock.now - began < Self.minRound { try await sleep(Self.minRound) }
            } catch {
                if Task.isCancelled || error is CancellationError { return }
                recoverAt = max(recoverAt ?? clock.now, clock.now)
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

    static func retry(after error: Error?) -> Duration? {
        guard let error else { return nil }
        guard let api = error as? APIError else { return nil }
        switch api {
        case .transport, .timedOut, .clockTimedOut: return .zero
        case .http(let status, _) where status >= 500: return .zero
        case .rateLimited(let after): return after
        default: return nil
        }
    }

    func execute(_ commands: [NowPlayingCommand], received: ContinuousClock.Instant = .now) async {
        var ran = false
        var skips = 0
        var track = TrackChanges()
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
            let skip = c.action == .next || c.action == .previous
            if skip { track.see(await bridge.trackID()) }
            guard await bridge.perform(c) else { continue }
            ran = true
            if skip { skips += 1 }
        }
        guard ran else { return }
        let info = skips > 0 ? await snapshot(after: skips, from: track) : await bridge.snapshot()
        guard let info, !Task.isCancelled else { return }
        pusher.submit(info)
    }

    struct TrackChanges {
        private(set) var current: String?
        private(set) var count = 0

        mutating func see(_ id: String?) {
            guard let id, !id.isEmpty else { return }
            if let current, current != id { count += 1 }
            current = id
        }
    }

    private func snapshot(after skips: Int, from start: TrackChanges) async -> MusicPlayerInfo? {
        var track = start
        var latest: MusicPlayerInfo?
        for poll in 0...Self.skipPolls {
            if poll > 0 {
                do { try await sleep(Self.skipPoll) } catch { return nil }
            }
            if let info = await bridge.snapshot() {
                latest = info
                track.see(info.persistentID)
                if track.count >= skips { break }
            } else if !(await bridge.isRunning()) {
                return nil
            }
        }
        return latest
    }
}
