import Foundation
import ImageIO
import Observation
import UniformTypeIdentifiers

public protocol MusicBridge: Sendable {
    func isRunning() async -> Bool
    func position() async -> Double?
    func artwork() async -> (trackID: String, data: Data)?
    func snapshot() async -> MusicPlayerInfo?
    /// nil when unknown (Music closed or the read failed), "" when no track is current.
    func trackID() async -> String?
    func volume() async -> Int?
    /// Address the running process only, never relaunch Music (ARCHITECTURE "Now playing" › Music).
    func perform(_ command: NowPlayingCommand) async -> Bool
    func canControl() async -> Bool
}

public enum MusicReply {
    public static func trackID(_ reply: NSAppleEventDescriptor) -> String? {
        reply.stringValue
    }
}

public protocol NowPlayingSink: Sendable {
    func report(_ r: NowPlayingReport) async throws -> NowPlayingAck
    func putArtwork(_ data: Data, contentType: String, source: String, player: String, trackID: String) async throws
}

public struct NowPlayingClient: NowPlayingSink {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func report(_ r: NowPlayingReport) async throws -> NowPlayingAck {
        try await client.request("POST", "/v1/nowplaying", body: r)
    }

    public func putArtwork(_ data: Data, contentType: String, source: String, player: String, trackID: String) async throws {
        try await client.putData("/v1/nowplaying/art", query: [
            URLQueryItem(name: "source", value: source), URLQueryItem(name: "player", value: player),
            URLQueryItem(name: "kind", value: "album"), URLQueryItem(name: "track_id", value: trackID),
        ], data: data, contentType: contentType)
    }
}

extension NowPlayingClient: NowPlayingCommandSource {
    public func commands(player: String, wait: Int, watched: Bool) async throws -> NowPlayingCommands {
        try await client.get("/v1/nowplaying/commands", query: [
            URLQueryItem(name: "player", value: player), URLQueryItem(name: "wait", value: String(wait)),
            URLQueryItem(name: "watched", value: watched ? "true" : "false"),
        ], budget: .clockLong)
    }
}

@MainActor
@Observable
public final class AppleMusicPusher {
    public static let source = "music"
    public static let seekPoll: Duration = .seconds(2)
    static let seekMissLimit = 3
    static let seekMaxSkips = 14
    static let seekTolerance: Int64 = 3000

    public typealias Sleep = @Sendable (Duration) async throws -> Void
    public typealias Now = @Sendable () -> ContinuousClock.Instant

    struct Anchor: Equatable {
        let positionMs: Int64
        let at: ContinuousClock.Instant

        func expected(at now: ContinuousClock.Instant) -> Int64 {
            let (s, a) = (now - at).components
            return positionMs + s * 1000 + a / 1_000_000_000_000_000
        }
    }

    public private(set) var lastError: String?
    public private(set) var lastSent: NowPlayingReport?
    public private(set) var watched = true

    @ObservationIgnored private let bridge: MusicBridge
    @ObservationIgnored private var sink: NowPlayingSink
    @ObservationIgnored private var player: String
    @ObservationIgnored private var latest: MusicPlayerInfo?
    @ObservationIgnored private var worker: Task<Void, Never>?
    @ObservationIgnored private var reportError: Error?
    @ObservationIgnored private(set) var anchor: Anchor?
    @ObservationIgnored private var seekWatch: Task<Void, Never>?
    @ObservationIgnored private var previewHolds = 0
    @ObservationIgnored private var seekMisses = 0
    @ObservationIgnored private var seekSkips = 0
    @ObservationIgnored private var submits = 0
    @ObservationIgnored private let seekEvery: Duration?
    @ObservationIgnored private let sleep: Sleep
    @ObservationIgnored private let now: Now

    public init(bridge: MusicBridge, sink: NowPlayingSink, player: String, seekPoll: Duration? = nil,
                sleep: @escaping Sleep = { try await Task.sleep(for: $0) },
                now: @escaping Now = { ContinuousClock.now }) {
        self.bridge = bridge
        self.sink = sink
        self.player = player
        seekEvery = seekPoll
        self.sleep = sleep
        self.now = now
    }

    public func configure(sink: NowPlayingSink, player: String) async {
        if player != self.player, let last = lastSent, last.state != .stopped {
            await drain()
            await send(MusicPlayerInfo(state: .stopped).report(source: Self.source, player: self.player))
        }
        self.sink = sink
        self.player = player
    }

    public func submit(_ info: MusicPlayerInfo) {
        submits += 1
        latest = info
        guard worker == nil else { return }
        worker = Task { [weak self] in
            while let self, let next = self.latest {
                self.latest = nil
                await self.push(next)
            }
            self?.worker = nil
        }
    }

    @discardableResult
    public func pushSnapshot() async -> Error? {
        guard await bridge.isRunning(), let info = await bridge.snapshot() else { return nil }
        submit(info)
        await drain()
        return reportError
    }

    public func stop() async {
        endSeekWatch()
        await drain()
        if let last = lastSent, last.state != .stopped {
            await send(MusicPlayerInfo(state: .stopped).report(source: Self.source, player: player))
        }
        anchor = nil
        endSeekWatch()
    }

    public func drain() async {
        while let w = worker { await w.value }
    }

    private func push(_ info: MusicPlayerInfo) async {
        var info = info
        var running = false
        if info.state != .stopped { running = await bridge.isRunning() }
        if running, info.position == nil { info.position = await bridge.position() }
        if running, info.volume == nil { info.volume = await bridge.volume() }
        let report = info.report(source: Self.source, player: player)
        guard let ack = await send(report), running, !ack.hasAlbumArt,
              !report.trackID.isEmpty,
              let raw = await bridge.artwork(), raw.trackID == report.trackID,
              let art = ArtworkShrinker.fit(raw.data) else { return }
        do {
            try await sink.putArtwork(art.data, contentType: art.contentType, source: Self.source,
                                      player: report.player, trackID: report.trackID)
        } catch {
            lastError = error.localizedDescription
        }
    }

    public func setWatched(_ on: Bool) {
        let was = watching
        watched = on
        watchingChanged(from: was)
    }

    public func holdPreview() {
        let was = watching
        previewHolds += 1
        watchingChanged(from: was)
    }

    public func releasePreview() {
        guard previewHolds > 0 else { return }
        let was = watching
        previewHolds -= 1
        watchingChanged(from: was)
    }

    var watching: Bool { watched || previewHolds > 0 }

    private func watchingChanged(from was: Bool) {
        guard watching != was else { return }
        guard watching else { return endSeekWatch() }
        seekMisses = 0
        seekSkips = 0
        watchSeeks(readFirst: true)
    }

    func checkSeek() async {
        let gen = submits
        guard worker == nil, let before = anchor else { return }
        if seekSkips > 0 {
            seekSkips -= 1
            if !(await bridge.isRunning()), current(gen, before) { submit(MusicPlayerInfo(state: .stopped)) }
            return
        }
        guard let pos = await bridge.position() else {
            if !(await bridge.isRunning()) {
                if current(gen, before) { submit(MusicPlayerInfo(state: .stopped)) }
            } else if current(gen, before) {
                seekMisses += 1
                if seekMisses >= Self.seekMissLimit {
                    seekSkips = min((1 << min(seekMisses - Self.seekMissLimit + 1, 8)) - 1, Self.seekMaxSkips)
                }
            }
            return
        }
        seekMisses = 0
        seekSkips = 0
        let at = now()
        guard current(gen, before),
              abs(Int64(max(pos, 0) * 1000) - before.expected(at: at)) > Self.seekTolerance,
              let info = await bridge.snapshot(), current(gen, before) else { return }
        submit(info)
    }

    var isWatchingSeeks: Bool { seekWatch != nil }

    private func current(_ gen: Int, _ before: Anchor) -> Bool {
        !Task.isCancelled && worker == nil && submits == gen && anchor == before
    }

    private func watchSeeks(readFirst: Bool = false) {
        guard watching, anchor != nil, let every = seekEvery, seekWatch == nil else { return }
        seekWatch = Task { [weak self, sleep, bridge] in
            if await bridge.canControl() {
                var wait = !readFirst
                while !Task.isCancelled {
                    if wait {
                        do { try await sleep(every) } catch { return }
                    }
                    guard let self else { return }
                    if !wait { await self.drain() }
                    wait = true
                    await self.checkSeek()
                }
            }
            if !Task.isCancelled { self?.seekWatch = nil }
        }
    }

    private func endSeekWatch() {
        seekWatch?.cancel()
        seekWatch = nil
    }

    @discardableResult
    private func send(_ report: NowPlayingReport) async -> NowPlayingAck? {
        if report.state == .playing {
            anchor = Anchor(positionMs: report.positionMs, at: now())
            seekMisses = 0
            seekSkips = 0
            watchSeeks()
        } else {
            anchor = nil
            endSeekWatch()
        }
        do {
            let ack = try await sink.report(report)
            lastSent = report
            lastError = nil
            reportError = nil
            return ack
        } catch {
            lastError = error.localizedDescription
            reportError = error
            return nil
        }
    }
}

public enum ArtworkShrinker {
    public static let maxBytes = 512 * 1024
    public static let maxSide = 1000

    public static func fit(_ data: Data) -> (data: Data, contentType: String)? {
        guard let src = CGImageSourceCreateWithData(data as CFData, nil),
              let type = CGImageSourceGetType(src) as String?,
              let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any],
              let w = props[kCGImagePropertyPixelWidth] as? Int, let h = props[kCGImagePropertyPixelHeight] as? Int
        else { return nil }
        let isPNG = type == UTType.png.identifier
        let isJPEG = type == UTType.jpeg.identifier
        if (isPNG || isJPEG), data.count <= maxBytes, max(w, h) <= maxSide {
            return (data, isPNG ? "image/png" : "image/jpeg")
        }
        let opts: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceThumbnailMaxPixelSize: maxSide,
            kCGImageSourceCreateThumbnailWithTransform: true,
        ]
        guard let thumb = CGImageSourceCreateThumbnailAtIndex(src, 0, opts as CFDictionary) else { return nil }
        let out = NSMutableData()
        guard let dest = CGImageDestinationCreateWithData(out, UTType.jpeg.identifier as CFString, 1, nil) else { return nil }
        CGImageDestinationAddImage(dest, thumb, [kCGImageDestinationLossyCompressionQuality: 0.85] as CFDictionary)
        guard CGImageDestinationFinalize(dest) else { return nil }
        return (out as Data, "image/jpeg")
    }
}
