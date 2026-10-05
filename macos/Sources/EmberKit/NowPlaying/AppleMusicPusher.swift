import Foundation
import ImageIO
import Observation
import UniformTypeIdentifiers

/// Reads Music.app. Implementations must never launch Music: every call
/// answers nil while it isn't running.
public protocol MusicBridge: Sendable {
    func isRunning() async -> Bool
    /// The player position in seconds.
    func position() async -> Double?
    /// The current track's artwork bytes as Music stores them (JPEG or PNG).
    func artwork() async -> Data?
    /// The whole player state, for when the pusher turns on mid-track.
    func snapshot() async -> MusicPlayerInfo?
}

/// Where reports and artwork go: the Ember server.
public protocol NowPlayingSink: Sendable {
    func report(_ r: NowPlayingReport) async throws -> NowPlayingAck
    func putArtwork(_ data: Data, contentType: String, source: String, player: String, trackID: String) async throws
}

/// `NowPlayingSink` over the server API.
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

/// Sends Music.app's state to Ember: one report per player notification,
/// plus the artwork whenever the server says it lacks it. Bursts (skipping
/// through tracks) coalesce to the latest state. No timers.
@MainActor
@Observable
public final class AppleMusicPusher {
    public static let source = "music"

    public private(set) var lastError: String?
    public private(set) var lastSent: NowPlayingReport?

    @ObservationIgnored private let bridge: MusicBridge
    @ObservationIgnored private var sink: NowPlayingSink
    @ObservationIgnored private var player: String
    @ObservationIgnored private var latest: MusicPlayerInfo?
    @ObservationIgnored private var worker: Task<Void, Never>?

    public init(bridge: MusicBridge, sink: NowPlayingSink, player: String) {
        self.bridge = bridge
        self.sink = sink
        self.player = player
    }

    /// Points the pusher at another server, or renames this Mac's player
    /// (the old name is reported stopped, so it doesn't linger).
    public func configure(sink: NowPlayingSink, player: String) async {
        if player != self.player, let last = lastSent, last.state != .stopped {
            await drain()
            await send(MusicPlayerInfo(state: .stopped).report(source: Self.source, player: self.player))
        }
        self.sink = sink
        self.player = player
    }

    /// Queues a player state; returns at once.
    public func submit(_ info: MusicPlayerInfo) {
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

    /// Reads Music once (when it runs) and pushes that, for a pusher that
    /// just turned on.
    public func pushSnapshot() async {
        guard await bridge.isRunning(), let info = await bridge.snapshot() else { return }
        submit(info)
        await drain()
    }

    /// Tells the server this Mac stopped playing (pusher turned off).
    public func stop() async {
        await drain()
        guard let last = lastSent, last.state != .stopped else { return }
        await send(MusicPlayerInfo(state: .stopped).report(source: Self.source, player: player))
    }

    /// Waits until queued states are sent.
    public func drain() async {
        while let w = worker { await w.value }
    }

    private func push(_ info: MusicPlayerInfo) async {
        var info = info
        var running = false
        if info.state != .stopped { running = await bridge.isRunning() }
        if running, info.position == nil { info.position = await bridge.position() }
        let report = info.report(source: Self.source, player: player)
        guard let ack = await send(report), running, !ack.hasAlbumArt,
              !report.trackID.isEmpty,
              let raw = await bridge.artwork(), let art = ArtworkShrinker.fit(raw) else { return }
        do {
            try await sink.putArtwork(art.data, contentType: art.contentType, source: Self.source,
                                      player: report.player, trackID: report.trackID)
        } catch {
            lastError = error.localizedDescription
        }
    }

    @discardableResult
    private func send(_ report: NowPlayingReport) async -> NowPlayingAck? {
        do {
            let ack = try await sink.report(report)
            lastSent = report
            lastError = nil
            return ack
        } catch {
            lastError = error.localizedDescription
            return nil
        }
    }
}

/// Keeps uploads small: artwork over `maxBytes` or `maxSide` is re-encoded
/// as a JPEG at most `maxSide` px on its longer side.
public enum ArtworkShrinker {
    public static let maxBytes = 512 * 1024
    public static let maxSide = 1000

    /// The bytes to upload and their content type; nil for unreadable data.
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
