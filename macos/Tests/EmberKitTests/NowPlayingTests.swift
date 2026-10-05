import Testing
import Foundation
import CoreGraphics
import ImageIO
import UniformTypeIdentifiers
@testable import EmberKit

private func golden(_ name: String) throws -> Data {
    let url = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("cmd/ember/testdata/dashboard/\(name).json")
    return try Data(contentsOf: url)
}

@Test func nowPlayingStateDecodesGoldens() async throws {
    let client = stubbedClient { req in (okResponse(req.url!), try golden("nowplaying_state")) }
    let s: NowPlayingState = try await client.get("/v1/nowplaying/state")
    #expect(s.isActive && s.state == "playing")
    #expect(s.title == "Teardrop" && s.artist == "Massive Attack" && s.album == "Mezzanine")
    #expect(s.durationMs == 330_000 && s.positionMs == 61_000)
    #expect(s.hasAlbumArt && !s.hasArtistArt && s.artVersion != nil)
    #expect(s.updatedAt != nil)

    let empty = stubbedClient { req in (okResponse(req.url!), try golden("nowplaying_state_none")) }
    let none: NowPlayingState = try await empty.get("/v1/nowplaying/state")
    #expect(!none.isActive && none.title == nil && none.artVersion == nil)
}

@Test func playerInfoParsesMusicNotification() throws {
    let info = try #require(MusicPlayerInfo(userInfo: [
        "Player State": "Playing", "Name": "Song", "Artist": "Band", "Album": "Record",
        "Total Time": NSNumber(value: 215_000), "PersistentID": NSNumber(value: Int64(-6_123_456_789)),
        "Location": "file:///private", "Store URL": "itms://x",
    ]))
    #expect(info.state == .playing && info.name == "Song" && info.durationMs == 215_000)
    #expect(info.persistentID == "FFFFFFFE930376EB")
    #expect(MusicPlayerInfo(userInfo: ["Name": "x"]) == nil)
    #expect(MusicPlayerInfo(userInfo: ["Player State": "Stopped"])?.state == .stopped)
}

@Test func reportCarriesOnlyWhatTheServerNeeds() throws {
    var info = MusicPlayerInfo(state: .paused, name: String(repeating: "é", count: 300), artist: "Band",
                               album: "Record", durationMs: 1000, persistentID: "AB", position: 12.5)
    let r = info.report(source: "music", player: "M4")
    #expect(r.positionMs == 12_500 && r.trackID == "AB" && r.title.unicodeScalars.count == 200)
    let body = String(decoding: try JSONEncoder().encode(r), as: UTF8.self)
    #expect(body.contains("\"track_id\":\"AB\"") && body.contains("\"position_ms\":12500"))
    #expect(!body.contains("Location"))
    info.state = .stopped
    #expect(info.report(source: "music", player: "M4") == NowPlayingReport(source: "music", player: "M4", state: .stopped))
}

// MARK: Pusher

private actor FakeBridge: MusicBridge {
    var running = true
    var art: Data? = Data([0xFF, 0xD8])
    var artReads = 0
    var positionReads = 0
    var snap: MusicPlayerInfo?
    func set(running: Bool) { self.running = running }
    func isRunning() async -> Bool { running }
    func position() async -> Double? { positionReads += 1; return running ? 42 : nil }
    func artwork() async -> Data? { artReads += 1; return running ? art : nil }
    func snapshot() async -> MusicPlayerInfo? { running ? snap : nil }
    func setSnapshot(_ s: MusicPlayerInfo) { snap = s }
    func setArt(_ d: Data?) { art = d }
}

private actor FakeSink: NowPlayingSink {
    var reports: [NowPlayingReport] = []
    var uploads: [String] = []
    var hasArt = false
    func report(_ r: NowPlayingReport) async throws -> NowPlayingAck {
        reports.append(r)
        return try JSONDecoder().decode(NowPlayingAck.self, from: Data(#"{"has_album_art":\#(hasArt),"has_artist_art":false}"#.utf8))
    }
    func putArtwork(_ data: Data, contentType: String, source: String, player: String, trackID: String) async throws {
        uploads.append(trackID)
        hasArt = true
    }
    func resetArt() { hasArt = false }
}

private func jpeg(side: Int) -> Data {
    let ctx = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0,
                        space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
    ctx.setFillColor(CGColor(red: 1, green: 0, blue: 0, alpha: 1))
    ctx.fill(CGRect(x: 0, y: 0, width: side, height: side))
    let out = NSMutableData()
    let dest = CGImageDestinationCreateWithData(out, UTType.png.identifier as CFString, 1, nil)!
    CGImageDestinationAddImage(dest, ctx.makeImage()!, nil)
    CGImageDestinationFinalize(dest)
    return out as Data
}

@MainActor
@Test func pusherSendsReportThenArtworkOncePerTrack() async {
    let bridge = FakeBridge(), sink = FakeSink()
    await bridge.setArt(jpeg(side: 64))
    let p = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    let song = MusicPlayerInfo(state: .playing, name: "Song", artist: "Band", album: "Record",
                               durationMs: 200_000, persistentID: "T1")
    p.submit(song)
    await p.drain()
    var paused = song
    paused.state = .paused
    p.submit(paused)
    await p.drain()

    let reports = await sink.reports
    #expect(reports.map(\.state) == [.playing, .paused])
    #expect(reports.first?.positionMs == 42_000)
    #expect(await sink.uploads == ["T1"])
    #expect(p.lastError == nil)
}

@MainActor
@Test func pusherNeverAsksMusicWhenItIsNotRunning() async {
    let bridge = FakeBridge(), sink = FakeSink()
    await bridge.set(running: false)
    let p = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    p.submit(MusicPlayerInfo(state: .stopped))
    await p.drain()
    p.submit(MusicPlayerInfo(state: .paused, name: "x", persistentID: "T1"))
    await p.drain()
    #expect(await bridge.artReads == 0)
    #expect(await bridge.positionReads == 0)
    #expect(await sink.reports.count == 2)
    await p.pushSnapshot()
    #expect(await sink.reports.count == 2)
}

@MainActor
@Test func pusherCoalescesBursts() async {
    let bridge = FakeBridge(), sink = FakeSink()
    await bridge.setArt(nil)
    let p = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    for i in 0..<5 {
        p.submit(MusicPlayerInfo(state: .playing, name: "t\(i)", persistentID: "T\(i)"))
    }
    await p.drain()
    let titles = await sink.reports.map(\.title)
    #expect(titles == ["t4"])
}

@MainActor
@Test func pusherStopSendsStoppedOnce() async {
    let bridge = FakeBridge(), sink = FakeSink()
    let p = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    await p.stop()
    #expect(await sink.reports.isEmpty)
    await bridge.setSnapshot(MusicPlayerInfo(state: .playing, name: "Song", persistentID: "T1", position: 3))
    await p.pushSnapshot()
    await p.stop()
    #expect(await sink.reports.map(\.state) == [.playing, .stopped])
    #expect(await sink.reports.first?.positionMs == 3000)
}

@Test func shrinkerKeepsSmallArtAndShrinksBigArt() throws {
    let small = jpeg(side: 300)
    let kept = try #require(ArtworkShrinker.fit(small))
    #expect(kept.data == small && kept.contentType == "image/png")
    let big = try #require(ArtworkShrinker.fit(jpeg(side: 1600)))
    #expect(big.contentType == "image/jpeg")
    let src = try #require(CGImageSourceCreateWithData(big.data as CFData, nil))
    let props = try #require(CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any])
    #expect(props[kCGImagePropertyPixelWidth] as? Int == ArtworkShrinker.maxSide)
    #expect(ArtworkShrinker.fit(Data("nope".utf8)) == nil)
}
