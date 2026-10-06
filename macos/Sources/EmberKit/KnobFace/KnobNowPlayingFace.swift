import CoreGraphics
import Foundation

public struct KnobPicture: Equatable, @unchecked Sendable {
    public let image: CGImage
    public let id: String

    public init(image: CGImage, id: String) {
        self.image = image; self.id = id
    }

    public static func == (a: KnobPicture, b: KnobPicture) -> Bool { a.id == b.id }
}

public struct KnobNowPlayingPictures: Equatable, Sendable {
    public var backdrop, album, artist: KnobPicture?

    public init(backdrop: KnobPicture? = nil, album: KnobPicture? = nil, artist: KnobPicture? = nil) {
        self.backdrop = backdrop; self.album = album; self.artist = artist
    }
}

public struct KnobNowPlayingFace: Equatable, Sendable {
    public enum Mode: Sendable, Equatable { case idle, playing, paused }

    public var mode: Mode
    public var idleLine: String
    public var title: String
    public var sub: String
    public var meta: String
    public var fraction: Double
    public var avatarFraction: Double
    public var pictures: KnobNowPlayingPictures

    public init(mode: Mode, idleLine: String = "", title: String = "", sub: String = "", meta: String = "",
                fraction: Double = 0, avatarFraction: Double? = nil, pictures: KnobNowPlayingPictures = .init()) {
        self.avatarFraction = avatarFraction ?? fraction
        self.mode = mode; self.idleLine = idleLine; self.title = title; self.sub = sub; self.meta = meta
        self.fraction = fraction; self.pictures = pictures
    }

    public init(state: NowPlayingState?, offline: Bool = false, now: Date, pictures: KnobNowPlayingPictures = .init()) {
        guard !offline, let s = state, s.isActive else {
            self.init(mode: .idle, idleLine: offline ? "Ember offline" : state == nil ? "Waiting for Ember" : "Nothing playing")
            return
        }
        let playing = s.state == "playing"
        let dur = max(s.durationMs ?? 0, 0)
        var pos = s.positionMs ?? 0
        if playing, let at = s.positionAt {
            pos += Int64((now.timeIntervalSince1970 * 1000).rounded()) - at
        }
        pos = max(0, dur > 0 ? min(pos, dur) : pos)
        pos = pos / 1000 * 1000

        let title = Self.fold(s.title ?? "")
        var sub = Self.fold(s.artist ?? "")
        if let album = s.album, !album.isEmpty {
            if !sub.isEmpty { sub += " - " }
            sub += Self.fold(album)
        }
        let played = Self.time(pos)
        let meta: String
        if !playing { meta = "PAUSED  \(played) / \(Self.time(dur))" }
        else if dur > 0 { meta = "\(Self.sourceName(s.source ?? ""))  \(played) / \(Self.time(dur))" }
        else { meta = "\(Self.sourceName(s.source ?? ""))  \(played)" }
        self.init(mode: playing ? .playing : .paused, title: title.isEmpty ? "Unknown track" : title, sub: sub, meta: meta,
                  fraction: dur > 0 ? min(Double(pos) / Double(dur), 1) : 0,
                  avatarFraction: dur > 0 ? min(Double(pos / 2000 * 2000) / Double(dur), 1) : 0, pictures: pictures)
    }

    static func time(_ ms: Int64) -> String {
        let s = max(ms, 0) / 1000
        return s >= 3600 ? String(format: "%lld:%02lld:%02lld", s / 3600, s / 60 % 60, s % 60)
                         : String(format: "%lld:%02lld", s / 60, s % 60)
    }

    static func sourceName(_ source: String) -> String {
        let name = source == "music" ? "Apple Music" : source == "plex" ? "Plex" : source
        return fold(name).uppercased()
    }

    static func fold(_ text: String) -> String { KnobTextFold.fold(text) }
}
