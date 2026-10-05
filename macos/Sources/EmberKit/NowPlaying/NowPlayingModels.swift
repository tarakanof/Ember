import Foundation

/// One player's state as `POST /v1/nowplaying` takes it.
public struct NowPlayingReport: Codable, Equatable, Sendable {
    public enum State: String, Codable, Sendable { case playing, paused, stopped }

    public var source: String
    public var player: String
    public var state: State
    public var title: String
    public var artist: String
    public var album: String
    public var trackID: String
    public var durationMs: Int64
    public var positionMs: Int64
    /// The player's own volume, 0-100 (Music's `sound volume`, not the
    /// system volume); left out of the JSON when unknown.
    public var volume: Int?

    public init(source: String, player: String, state: State, title: String = "", artist: String = "",
                album: String = "", trackID: String = "", durationMs: Int64 = 0, positionMs: Int64 = 0,
                volume: Int? = nil) {
        self.source = source; self.player = player; self.state = state
        self.title = title; self.artist = artist; self.album = album; self.trackID = trackID
        self.durationMs = durationMs; self.positionMs = positionMs; self.volume = volume
    }

    enum CodingKeys: String, CodingKey {
        case source, player, state, title, artist, album, volume
        case trackID = "track_id", durationMs = "duration_ms", positionMs = "position_ms"
    }
}

/// The server's answer to a report: which pictures it already holds.
public struct NowPlayingAck: Decodable, Equatable, Sendable {
    public let hasAlbumArt: Bool
    public let hasArtistArt: Bool

    enum CodingKeys: String, CodingKey {
        case hasAlbumArt = "has_album_art", hasArtistArt = "has_artist_art"
    }
}

/// `GET /v1/nowplaying/state`. `state` is playing, paused or none;
/// `positionMs` is the position at `positionAt` (Unix ms).
public struct NowPlayingState: Decodable, Equatable, Sendable {
    public let state: String
    public let source: String?
    public let title: String?
    public let artist: String?
    public let album: String?
    public let durationMs: Int64?
    public let positionMs: Int64?
    public let positionAt: Int64?
    public let updatedAt: Date?
    public let artVersion: String?
    public let hasAlbumArt: Bool
    public let hasArtistArt: Bool

    enum CodingKeys: String, CodingKey {
        case state, source, title, artist, album
        case durationMs = "duration_ms", positionMs = "position_ms", positionAt = "position_at"
        case updatedAt = "updated_at", artVersion = "art_version"
        case hasAlbumArt = "has_album_art", hasArtistArt = "has_artist_art"
    }

    /// Whether something is playing or paused.
    public var isActive: Bool { state != "none" }
}

/// Music.app's `com.apple.Music.playerInfo` distributed notification,
/// parsed. Apple doesn't document the keys; the ones read here are stable
/// since iTunes.
public struct MusicPlayerInfo: Equatable, Sendable {
    public var state: NowPlayingReport.State
    public var name: String
    public var artist: String
    public var album: String
    /// Milliseconds; 0 when unknown (streams).
    public var durationMs: Int64
    /// The track's persistent ID as Music's AppleScript prints it (16 upper-case hex digits).
    public var persistentID: String
    /// Seconds, when the source knew it (AppleScript snapshot only).
    public var position: Double?
    /// Music's `sound volume` (0-100), when read (AppleScript only).
    public var volume: Int?

    public init(state: NowPlayingReport.State, name: String = "", artist: String = "", album: String = "",
                durationMs: Int64 = 0, persistentID: String = "", position: Double? = nil, volume: Int? = nil) {
        self.state = state; self.name = name; self.artist = artist; self.album = album
        self.durationMs = durationMs; self.persistentID = persistentID; self.position = position
        self.volume = volume
    }

    /// Parses a notification's userInfo; nil when it has no player state.
    public init?(userInfo: [AnyHashable: Any]) {
        guard let raw = userInfo["Player State"] as? String else { return nil }
        switch raw.lowercased() {
        case "playing": state = .playing
        case "paused": state = .paused
        default: state = .stopped
        }
        name = userInfo["Name"] as? String ?? ""
        artist = userInfo["Artist"] as? String ?? ""
        album = userInfo["Album"] as? String ?? ""
        durationMs = (userInfo["Total Time"] as? NSNumber)?.int64Value ?? 0
        persistentID = (userInfo["PersistentID"] as? NSNumber).map { Self.hexID($0.int64Value) } ?? ""
        position = nil
        volume = nil
    }

    /// The notification's signed 64-bit id in AppleScript's hex form.
    public static func hexID(_ id: Int64) -> String {
        String(format: "%016llX", UInt64(bitPattern: id))
    }

    /// The report for this state, from `source` on `player`.
    public func report(source: String, player: String) -> NowPlayingReport {
        let player = Self.clip(player, 64)
        guard state != .stopped else { return NowPlayingReport(source: source, player: player, state: .stopped) }
        return NowPlayingReport(
            source: source, player: player, state: state,
            title: Self.clip(name, 200), artist: Self.clip(artist, 200), album: Self.clip(album, 200),
            trackID: persistentID, durationMs: max(durationMs, 0),
            positionMs: position.map { Int64(max($0, 0) * 1000) } ?? 0,
            volume: volume.map { min(max($0, 0), 100) })
    }

    /// Cuts to n Unicode scalars, the unit the server counts.
    static func clip(_ s: String, _ n: Int) -> String {
        s.unicodeScalars.count <= n ? s : String(String.UnicodeScalarView(s.unicodeScalars.prefix(n)))
    }
}

/// One playback command for this Mac's Music, from
/// `GET /v1/nowplaying/commands` (the knob's controls, Ember #280).
public struct NowPlayingCommand: Decodable, Equatable, Sendable {
    public enum Action: String, Sendable { case play, pause, playPause = "play_pause", next, previous, volume }

    public let id: String
    /// nil for an action this app doesn't know (a newer server): ignored.
    public let action: Action?
    /// Volume change in points (volume only).
    public let delta: Int
    /// How long the server had held it when it answered the poll (ms).
    public let ageMs: Int

    public init(id: String, action: Action?, delta: Int = 0, ageMs: Int = 0) {
        self.id = id; self.action = action; self.delta = delta; self.ageMs = ageMs
    }

    enum CodingKeys: String, CodingKey { case id, action, delta, ageMs = "age_ms" }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        action = try c.decodeIfPresent(String.self, forKey: .action).flatMap(Action.init(rawValue:))
        delta = try c.decodeIfPresent(Int.self, forKey: .delta) ?? 0
        ageMs = max(0, try c.decodeIfPresent(Int.self, forKey: .ageMs) ?? 0)
    }
}

/// The answer of `GET /v1/nowplaying/commands`.
public struct NowPlayingCommands: Decodable, Equatable, Sendable {
    public let commands: [NowPlayingCommand]

    enum CodingKeys: String, CodingKey { case commands }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        commands = try c.decodeIfPresent([NowPlayingCommand].self, forKey: .commands) ?? []
    }
}
