import Foundation

/// The knob face's geometry, palette, layout and font sizes, decoded from
/// `knob-theme.json`: the one copy of the numbers cinder's firmware draws with.
public struct KnobTheme: Decodable, Sendable, Equatable {
    public struct Screen: Decodable, Sendable, Equatable {
        public var diameterPx: Double
    }

    public struct Font: Decodable, Sendable, Equatable {
        public var family: String
    }

    public struct MoodColors: Decodable, Sendable, Equatable {
        public var idle, working, waiting, error, done: RGB
    }

    public struct Bot: Decodable, Sendable, Equatable {
        public struct Triangle: Decodable, Sendable, Equatable {
            public var radius, sagitta: Double
            public var tableBins: Int
            public var blurDeg: Double
        }
        public struct Hop: Decodable, Sendable, Equatable {
            public var durationS, squash, intervalMedianS, intervalSigma, intervalMinS, intervalMaxS: Double
        }
        public struct Badge: Decodable, Sendable, Equatable {
            public var at, gap, dot: Double
        }
        public struct Host: Decodable, Sendable, Equatable {
            public var fontPx, y, widthPx: Double
            public var maxChars: Int
        }
        public var fill: Double
        public var bodyColor, eyeColor: RGB
        public var eyeScale, hopScale, rimPx, rimDimGain: Double
        public var ringPoints: Int
        public var triangle: Triangle
        public var hop: Hop
        public var badge: Badge
        public var host: Host
    }

    public struct Label: Decodable, Sendable, Equatable {
        public var fontPx, dyPx, widthPx: Double
    }

    public struct Pomodoro: Decodable, Sendable, Equatable {
        public struct Colors: Decodable, Sendable, Equatable {
            public var focus, `break`, other, idleTrack, text, textDim, note: RGB
        }
        public var ringRadiusPx, ringWidthPx, trackGain, pausedGain: Double
        public var colors: Colors
        public var time, phase, round: Label
    }

    public struct Weather: Decodable, Sendable, Equatable {
        public struct Sky: Decodable, Sendable, Equatable {
            public var widthPx, heightPx, yPx: Double
        }
        public struct Temp: Decodable, Sendable, Equatable {
            public var fontPx: Double
            public var color, stillColor: RGB
        }
        public struct Colors: Decodable, Sendable, Equatable {
            public var sun, rays, moon, star, cloud, rainCloud, stormCloud, litCloud, snowCloud: RGB
            public var rain, snow, bolt, fog, rime, still: RGB
        }
        public var sky: Sky
        public var temp: Temp
        public var maxAgeS: Double
        public var colors: Colors
    }

    public var version: Int
    public var screen: Screen
    public var font: Font
    public var moodColors: MoodColors
    public var bot: Bot
    public var pomodoro: Pomodoro
    public var weather: Weather

    /// The theme shipped in EmberKit's bundle.
    public static let standard: KnobTheme = {
        do {
            return try load()
        } catch {
            preconditionFailure("knob-theme.json: \(error)")
        }
    }()

    /// Decodes EmberKit's `knob-theme.json`; throws if it is missing or malformed.
    public static func load() throws -> KnobTheme {
        guard let url = Bundle.module.url(forResource: "knob-theme", withExtension: "json") else {
            throw CocoaError(.fileNoSuchFile)
        }
        return try decode(Data(contentsOf: url))
    }

    /// Decodes a theme document; throws on a missing key or a bad colour.
    public static func decode(_ data: Data) throws -> KnobTheme {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return try d.decode(KnobTheme.self, from: data)
    }

    /// The outline and badge colour for a mood (Ember's `stateColorRGB`).
    public func moodColor(_ mood: BotMood) -> RGB {
        switch mood {
        case .idle, .sleepy: moodColors.idle
        case .working: moodColors.working
        case .waiting: moodColors.waiting
        case .error: moodColors.error
        case .done: moodColors.done
        }
    }

    /// The knob's bot timings.
    public var botTuning: BotBehavior.Tuning {
        let h = bot.hop
        return BotBehavior.Tuning(sleepAfter: BotBehavior.sleepAfter, hopLength: h.durationS, hopSquash: h.squash,
                                  hopIntervalMedian: h.intervalMedianS, hopIntervalSigma: h.intervalSigma,
                                  hopIntervalRange: h.intervalMinS...h.intervalMaxS)
    }
}

extension RGB: Decodable {
    public init(from decoder: Decoder) throws {
        let s = try decoder.singleValueContainer().decode(String.self)
        guard let c = RGB(hex: s) else {
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath,
                                                    debugDescription: "\(s) is not #RRGGBB"))
        }
        self = c
    }

    /// Each channel times `gain` (0…1), the way the firmware dims a colour on black.
    public func scaled(_ gain: Double) -> RGB {
        func s(_ v: UInt8) -> UInt8 { UInt8(min(255, max(0, Double(v) * gain))) }
        return RGB(r: s(r), g: s(g), b: s(b))
    }
}
