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

private actor FakeBridge: MusicBridge {
    var running = true
    var art: Data? = Data([0xFF, 0xD8])
    var artTrack: String?
    var artReads = 0
    var positionReads = 0
    var snap: MusicPlayerInfo?
    func set(running: Bool) { self.running = running }
    func isRunning() async -> Bool { running }
    func position() async -> Double? { positionReads += 1; return running ? 42 : nil }
    func artwork() async -> (trackID: String, data: Data)? {
        artReads += 1
        guard running, let art else { return nil }
        return (artTrack ?? snap?.persistentID ?? "T1", art)
    }
    func setArtTrack(_ id: String) { artTrack = id }
    func snapshot() async -> MusicPlayerInfo? { running ? snap : nil }
    func setSnapshot(_ s: MusicPlayerInfo) { snap = s }
    func setArt(_ d: Data?) { art = d }
    var level = 40
    var granted = true
    func set(granted: Bool) { self.granted = granted }
    func canControl() async -> Bool { granted }
    var performed: [NowPlayingCommand] = []
    func volume() async -> Int? { running ? level : nil }
    func perform(_ command: NowPlayingCommand) async -> Bool {
        guard running else { return false }
        performed.append(command)
        if command.action == .volume { level = min(max(level + command.delta, 0), 100) }
        snap?.volume = level
        return true
    }
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

@MainActor
@Test func renamingThePlayerStopsTheOldOne() async {
    let bridge = FakeBridge(), sink = FakeSink()
    await bridge.setArt(nil)
    let p = AppleMusicPusher(bridge: bridge, sink: sink, player: "Old")
    p.submit(MusicPlayerInfo(state: .playing, name: "Song", persistentID: "T1"))
    await p.drain()
    await p.configure(sink: sink, player: "New")
    let r = await sink.reports
    #expect(r.map(\.player) == ["Old", "Old"] && r.last?.state == .stopped)
    await p.configure(sink: sink, player: "New")
    #expect(await sink.reports.count == 2)
}

@MainActor
@Test func pusherSkipsArtworkOfAnotherTrack() async {
    let bridge = FakeBridge(), sink = FakeSink()
    await bridge.setArt(jpeg(side: 32))
    await bridge.setArtTrack("T2")
    let p = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    p.submit(MusicPlayerInfo(state: .playing, name: "A", persistentID: "T1"))
    await p.drain()
    #expect(await sink.uploads.isEmpty)
}

@Test func reportCarriesVolumeOnlyWhenKnown() throws {
    let without = String(decoding: try JSONEncoder().encode(
        NowPlayingReport(source: "music", player: "M4", state: .playing)), as: UTF8.self)
    #expect(!without.contains("volume"))
    let with = MusicPlayerInfo(state: .playing, name: "x", volume: 130).report(source: "music", player: "M4")
    #expect(with.volume == 100)
    #expect(String(decoding: try JSONEncoder().encode(with), as: UTF8.self).contains("\"volume\":100"))
}

@Test func commandsDecodeAndToleratesUnknownActions() throws {
    let body = #"{"commands":[{"id":"c1","action":"next"},{"id":"c2","action":"volume","delta":-4},{"id":"c3","action":"shuffle"}]}"#
    let c = try JSONDecoder().decode(NowPlayingCommands.self, from: Data(body.utf8)).commands
    #expect(c == [NowPlayingCommand(id: "c1", action: .next), NowPlayingCommand(id: "c2", action: .volume, delta: -4),
                  NowPlayingCommand(id: "c3", action: nil)])
    #expect(try JSONDecoder().decode(NowPlayingCommands.self, from: Data("{}".utf8)).commands.isEmpty)
}

@Test func commandSourceLongPollsTheServer() async throws {
    let client = stubbedClient { req in
        #expect(req.url?.path == "/v1/nowplaying/commands")
        let q = URLComponents(url: req.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        #expect(q.contains(URLQueryItem(name: "player", value: "M4")) && q.contains(URLQueryItem(name: "wait", value: "25")))
        return (okResponse(req.url!), Data(#"{"commands":[{"id":"c1","action":"play_pause"}]}"#.utf8))
    }
    let got = try await NowPlayingClient(client: client).commands(player: "M4", wait: 25)
    #expect(got.map(\.action) == [.playPause])
}

private actor FakeCommands: NowPlayingCommandSource {
    var rounds: [Result<[NowPlayingCommand], Error>]
    var calls = 0
    init(_ rounds: [Result<[NowPlayingCommand], Error>]) { self.rounds = rounds }
    func commands(player: String, wait: Int) async throws -> [NowPlayingCommand] {
        calls += 1
        if rounds.isEmpty {
            try await Task.sleep(for: .seconds(3600))
            return []
        }
        return try rounds.removeFirst().get()
    }
}

private final class Sleeps: @unchecked Sendable {
    private let lock = NSLock()
    private var log: [Duration] = []
    var all: [Duration] { lock.withLock { log } }
    func sleep(_ d: Duration) async throws {
        lock.withLock { log.append(d) }
        try Task.checkCancellation()
    }
}

@MainActor
private func waitUntil(_ cond: () async -> Bool) async {
    for _ in 0..<500 where !(await cond()) { try? await Task.sleep(for: .milliseconds(2)) }
}

@MainActor
@Test func listenerRunsCommandsInOrderAndReReportsVolume() async {
    let bridge = FakeBridge(), sink = FakeSink()
    await bridge.setArt(nil)
    await bridge.setSnapshot(MusicPlayerInfo(state: .playing, name: "Song", persistentID: "T1"))
    let pusher = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    let sleeps = Sleeps()
    let listener = MusicCommandListener(bridge: bridge, pusher: pusher, sleep: sleeps.sleep)
    let source = FakeCommands([.success([
        NowPlayingCommand(id: "1", action: .next), NowPlayingCommand(id: "2", action: nil),
        NowPlayingCommand(id: "3", action: .volume, delta: 6),
    ])])
    listener.start(source: source, player: "M4")
    await waitUntil { await source.calls >= 2 }
    await pusher.drain()
    #expect(await bridge.performed.map(\.id) == ["1", "3"])
    #expect(await sink.reports.last?.volume == 46)
    listener.stop()
    await listener.join()
}

@MainActor
@Test func listenerNeverPerformsWhileMusicIsClosed() async {
    let bridge = FakeBridge(), sink = FakeSink()
    await bridge.set(running: false)
    let pusher = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    let listener = MusicCommandListener(bridge: bridge, pusher: pusher, sleep: Sleeps().sleep)
    let source = FakeCommands([.success([NowPlayingCommand(id: "1", action: .playPause)])])
    listener.start(source: source, player: "M4")
    await waitUntil { await source.calls >= 2 }
    #expect(await bridge.performed.isEmpty)
    #expect(await sink.reports.isEmpty)
    listener.stop()
    await listener.join()
}

@MainActor
@Test func listenerDropsStaleCommandsAndNeedsAutomation() async {
    let bridge = FakeBridge(), sink = FakeSink()
    let pusher = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    let listener = MusicCommandListener(bridge: bridge, pusher: pusher, sleep: Sleeps().sleep)
    await listener.execute([NowPlayingCommand(id: "old", action: .next, ageMs: 5_001),
                            NowPlayingCommand(id: "ok", action: .pause, ageMs: 200)])
    #expect(await bridge.performed.map(\.id) == ["ok"])
    await listener.execute([NowPlayingCommand(id: "late", action: .play)], received: .now - .seconds(6))
    #expect(await bridge.performed.map(\.id) == ["ok"])
    await bridge.set(granted: false)
    await listener.execute([NowPlayingCommand(id: "denied", action: .play)])
    #expect(await bridge.performed.map(\.id) == ["ok"])
}

@Test func commandDecodesPlayPauseAndAge() throws {
    let json = Data(#"{"commands":[{"id":"a","action":"play","age_ms":120},{"id":"b","action":"pause"},{"id":"c","action":"play_pause","age_ms":-4}]}"#.utf8)
    let c = try JSONDecoder().decode(NowPlayingCommands.self, from: json).commands
    #expect(c.map(\.action) == [.play, .pause, .playPause])
    #expect(c.map(\.ageMs) == [120, 0, 0])
}

@MainActor
@Test func listenerBacksOffOnErrors() async {
    let bridge = FakeBridge(), sink = FakeSink()
    let pusher = AppleMusicPusher(bridge: bridge, sink: sink, player: "M4")
    let sleeps = Sleeps()
    let listener = MusicCommandListener(bridge: bridge, pusher: pusher, sleep: sleeps.sleep)
    let down = APIError.transport("down")
    let source = FakeCommands([.failure(down), .failure(down), .failure(down), .failure(down), .failure(down),
                               .failure(APIError.http(status: 404, body: "")), .success([]), .failure(down)])
    listener.start(source: source, player: "M4")
    await waitUntil { await source.calls >= 9 }
    #expect(sleeps.all == [.seconds(5), .seconds(10), .seconds(20), .seconds(40), .seconds(60), .seconds(300),
                           .seconds(1), .seconds(5)])
    listener.stop()
    await listener.join()
}
