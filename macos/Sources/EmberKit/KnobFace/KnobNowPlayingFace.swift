import CoreGraphics
import Foundation

/// A decoded picture for the now-playing face; equal when the server's
/// `art_version` and kind are.
public struct KnobPicture: Equatable, @unchecked Sendable {
    public let image: CGImage
    public let id: String

    public init(image: CGImage, id: String) {
        self.image = image; self.id = id
    }

    public static func == (a: KnobPicture, b: KnobPicture) -> Bool { a.id == b.id }
}

/// The three pictures the knob fetches for a track (`kind=backdrop|album|artist`).
public struct KnobNowPlayingPictures: Equatable, Sendable {
    public var backdrop, album, artist: KnobPicture?

    public init(backdrop: KnobPicture? = nil, album: KnobPicture? = nil, artist: KnobPicture? = nil) {
        self.backdrop = backdrop; self.album = album; self.artist = artist
    }
}

/// The knob's now-playing page for one moment, as cinder's `nowplaying_view.c`
/// draws it. Text is already folded to ASCII (Montserrat has no other glyphs);
/// the renderer cuts each line to its box with "...".
public struct KnobNowPlayingFace: Equatable, Sendable {
    public enum Mode: Sendable, Equatable { case idle, playing, paused }

    public var mode: Mode
    /// The grey line of the idle face ("Nothing playing").
    public var idleLine: String
    public var title: String
    /// "Artist - Album".
    public var sub: String
    /// "APPLE MUSIC  1:02 / 3:10", or "PAUSED  1:02 / 3:10".
    public var meta: String
    /// Played share 0…1, from the shown whole second.
    public var fraction: Double
    public var pictures: KnobNowPlayingPictures

    public init(mode: Mode, idleLine: String = "", title: String = "", sub: String = "", meta: String = "",
                fraction: Double = 0, pictures: KnobNowPlayingPictures = .init()) {
        self.mode = mode; self.idleLine = idleLine; self.title = title; self.sub = sub; self.meta = meta
        self.fraction = fraction; self.pictures = pictures
    }

    /// The face for the server's `state` (nil: not read yet) at `now`. `offline`
    /// is the knob's "Ember unreachable" case: it shows the link, not the last track.
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
        pos = pos / 1000 * 1000   // the knob moves on whole seconds

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
                  fraction: dur > 0 ? min(Double(pos) / Double(dur), 1) : 0, pictures: pictures)
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

    /// The firmware's `np_text_fold`: Latin diacritics dropped, Cyrillic and
    /// other scripts transliterated, typographic punctuation to ASCII, the rest "?".
    static func fold(_ text: String) -> String {
        var out = ""
        for ch in text {
            for u in String(ch).unicodeScalars {
                if u.value < 0x80 { out.unicodeScalars.append(u.value < 0x20 || u.value == 0x7F ? " " : u); continue }
                if let m = special[u.value] { out += m; continue }
                let v = u.value
                guard (0xC0...0x24F).contains(v) || (0x1E00...0x1EFF).contains(v) || (0x400...0x52F).contains(v) else {
                    out += "?"; continue
                }
                let latin = String(u).applyingTransform(.toLatin, reverse: false) ?? String(u)
                let plain = (latin.applyingTransform(.stripCombiningMarks, reverse: false) ?? latin)
                out += plain.unicodeScalars.allSatisfy { $0.value < 0x80 } && !plain.isEmpty ? plain : "?"
            }
        }
        return out
    }

    private static let special: [UInt32: String] = [
        0x2018: "'", 0x2019: "'", 0x201A: ",", 0x201C: "\"", 0x201D: "\"", 0x201E: "\"", 0x2013: "-", 0x2014: "-",
        0x2026: "...", 0x00A0: " ", 0x00B0: "\u{B0}", 0x2022: "\u{2022}", 0x00DF: "ss", 0x00C6: "AE", 0x00E6: "ae",
        0x00D8: "O", 0x00F8: "o", 0x0110: "D", 0x0111: "d", 0x0141: "L", 0x0142: "l", 0x0152: "OE", 0x0153: "oe",
        0x00DE: "Th", 0x00FE: "th", 0x00D0: "D", 0x00F0: "d",
    ]
}
