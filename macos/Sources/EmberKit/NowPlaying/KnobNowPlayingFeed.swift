import Foundation
import ImageIO

@MainActor
@Observable
public final class KnobNowPlayingFeed {
    public static let offlineAfterFailures = 3

    public private(set) var state: NowPlayingState?
    public private(set) var failures = 0
    public private(set) var pictures = KnobNowPlayingPictures()
    public private(set) var serverOffset: TimeInterval = 0
    public private(set) var artError: Error?

    public var failed: Bool { failures >= Self.offlineAfterFailures }

    public typealias StateRead = (state: NowPlayingState, serverNow: Date?)

    private let fetchState: @Sendable () async throws -> StateRead
    private let fetchArt: @Sendable (_ kind: String, _ size: Int, _ version: String) async throws -> Data
    private let now: @Sendable () -> Date
    private let interval: Duration
    private let sizes: [(kind: String, size: Int)]
    private var version: String?
    private var loaded: [String: KnobPicture] = [:]
    private var retryAt: Date?
    private var retryDelay: TimeInterval = KnobNowPlayingFeed.firstRetry

    static let firstRetry: TimeInterval = 5
    static let lastRetry: TimeInterval = 60

    public init(thumbnail: Bool = false,
                fetchState: @escaping @Sendable () async throws -> StateRead,
                fetchArt: @escaping @Sendable (_ kind: String, _ size: Int, _ version: String) async throws -> Data,
                now: @escaping @Sendable () -> Date = Date.init, interval: Duration = .seconds(3)) {
        self.fetchState = fetchState; self.fetchArt = fetchArt; self.now = now; self.interval = interval
        sizes = thumbnail ? [("album", 120), ("artist", 64)] : [("backdrop", 466), ("album", 240), ("artist", 64)]
    }

    public convenience init(client: APIClient, thumbnail: Bool = false) {
        self.init(thumbnail: thumbnail,
                  fetchState: {
                      let (s, http): (NowPlayingState, HTTPURLResponse) = try await client.getWithResponse("/v1/nowplaying/state")
                      return (s, http.value(forHTTPHeaderField: "X-Ember-Now").flatMap(TimeInterval.init).map { Date(timeIntervalSince1970: $0) })
                  },
                  fetchArt: { kind, size, version in
                      try await client.getData("/v1/nowplaying/art", query: [
                          URLQueryItem(name: "kind", value: kind), URLQueryItem(name: "size", value: String(size)),
                          URLQueryItem(name: "v", value: version)])
                  })
    }

    public func run() async {
        while !Task.isCancelled {
            await refresh()
            try? await Task.sleep(for: interval)
        }
    }

    public func refresh() async {
        let asked = now()
        do {
            let read = try await fetchState()
            state = read.state
            failures = 0
            if let server = read.serverNow {
                let skew = server.timeIntervalSince1970 + 0.5 - asked.timeIntervalSince1970
                serverOffset = abs(skew) > 2 ? skew : 0
            }
            await loadPictures(for: read.state)
        } catch is CancellationError {
            return
        } catch {
            failures += 1
        }
    }

    private func loadPictures(for s: NowPlayingState) async {
        guard s.isActive, let v = s.artVersion, s.hasAlbumArt || s.hasArtistArt else {
            version = nil; loaded = [:]; artError = nil; retryAt = nil; retryDelay = Self.firstRetry
            pictures = .init()
            return
        }
        if version != v {
            version = v; loaded = [:]; artError = nil; retryAt = nil; retryDelay = Self.firstRetry
        }
        let wanted = sizes.filter { $0.kind != "album" || s.hasAlbumArt }.filter { $0.kind != "artist" || s.hasArtistArt }
        for (kind, size) in wanted where loaded[kind] == nil {
            if let at = retryAt, now() < at { break }
            do {
                let data = try await fetchArt(kind, size, v)
                guard let image = Self.decode(data) else { throw APIError.decoding("\(kind) picture") }
                guard version == v, !Task.isCancelled else { return }
                loaded[kind] = KnobPicture(image: image, id: "\(kind)-\(size)-\(v)")
            } catch is CancellationError {
                return
            } catch {
                artError = error
                retryAt = now().addingTimeInterval(retryDelay)
                retryDelay = min(retryDelay * 2, Self.lastRetry)
                break
            }
        }
        if wanted.allSatisfy({ loaded[$0.kind] != nil }) { artError = nil; retryAt = nil }
        pictures = KnobNowPlayingPictures(backdrop: loaded["backdrop"], album: loaded["album"], artist: loaded["artist"])
    }

    static func decode(_ data: Data) -> CGImage? {
        guard let src = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
        return CGImageSourceCreateImageAtIndex(src, 0, nil)
    }
}
