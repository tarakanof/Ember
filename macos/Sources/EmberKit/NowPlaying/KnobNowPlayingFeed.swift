import Foundation
import ImageIO

/// What the now-playing knob preview reads: `GET /v1/nowplaying/state`, and
/// the three pictures the knob fetches (`GET /v1/nowplaying/art`), re-fetched
/// only when `art_version` moves. Runs only while a preview is on screen.
@MainActor
@Observable
public final class KnobNowPlayingFeed {
    public private(set) var state: NowPlayingState?
    /// The last read failed (the knob would say "Ember offline" after three).
    public private(set) var failed = false
    public private(set) var pictures = KnobNowPlayingPictures()

    private let fetchState: @Sendable () async throws -> NowPlayingState
    private let fetchArt: @Sendable (_ kind: String, _ size: Int, _ version: String) async throws -> Data
    private var picturesFor: String?
    private let interval: Duration

    public init(fetchState: @escaping @Sendable () async throws -> NowPlayingState,
                fetchArt: @escaping @Sendable (_ kind: String, _ size: Int, _ version: String) async throws -> Data,
                interval: Duration = .seconds(3)) {
        self.fetchState = fetchState; self.fetchArt = fetchArt; self.interval = interval
    }

    public convenience init(client: APIClient) {
        self.init(fetchState: { try await client.get("/v1/nowplaying/state") },
                  fetchArt: { kind, size, version in
                      try await client.getData("/v1/nowplaying/art", query: [
                          URLQueryItem(name: "kind", value: kind), URLQueryItem(name: "size", value: String(size)),
                          URLQueryItem(name: "v", value: version)])
                  })
    }

    /// The pictures the knob draws, [kind: size].
    static let sizes = [("backdrop", 466), ("album", 240), ("artist", 64)]

    /// Polls until cancelled.
    public func run() async {
        while !Task.isCancelled {
            await refresh()
            try? await Task.sleep(for: interval)
        }
    }

    public func refresh() async {
        do {
            let s = try await fetchState()
            state = s
            failed = false
            await loadPictures(for: s)
        } catch is CancellationError {
            return
        } catch {
            failed = true
        }
    }

    private func loadPictures(for s: NowPlayingState) async {
        guard s.isActive, let version = s.artVersion, s.hasAlbumArt || s.hasArtistArt else {
            picturesFor = nil
            pictures = .init()
            return
        }
        guard picturesFor != version else { return }
        picturesFor = version
        var out = KnobNowPlayingPictures()
        for (kind, size) in Self.sizes {
            if kind == "album", !s.hasAlbumArt { continue }
            if kind == "artist", !s.hasArtistArt { continue }
            guard let data = try? await fetchArt(kind, size, version), let image = Self.decode(data) else { continue }
            let pic = KnobPicture(image: image, id: "\(kind)-\(version)")
            switch kind {
            case "backdrop": out.backdrop = pic
            case "album": out.album = pic
            default: out.artist = pic
            }
        }
        if Task.isCancelled { picturesFor = nil; return }
        pictures = out
    }

    static func decode(_ data: Data) -> CGImage? {
        guard let src = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
        return CGImageSourceCreateImageAtIndex(src, 0, nil)
    }
}
