import Foundation

public struct KnobTheme: Decodable, Sendable, Equatable {
    public struct Screen: Decodable, Sendable, Equatable {
        public var diameterPx: Double
    }

    public struct Font: Decodable, Sendable, Equatable {
        public struct Metrics: Decodable, Sendable, Equatable {
            public var lineHeightPx, baselinePx: Double
        }
        public var family: String
        public var file: String
        public var metrics: [String: Metrics]

        public func metrics(_ px: Double) -> Metrics {
            if let m = metrics[String(Int(px))] { return m }
            let k = px / 48
            return Metrics(lineHeightPx: 52 * k, baselinePx: 9 * k)
        }
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
        public struct Eyes: Decodable, Sendable, Equatable {
            public struct Capsule: Decodable, Sendable, Equatable {
                public var width, height, angleDeg: [Double]
            }
            public struct Happy: Decodable, Sendable, Equatable {
                public var width: Double
                public var height: [Double]
                public var lift, stroke: Double
                public var points: Int
            }
            public var restX, reach, reachTriangle, dropTriangle, dropSlump, limit, limitTriangle: Double
            public var foreshorten, straight, leanAt, sep, sepRound, rise, minHalf: Double
            public var dash, angry, round: Capsule
            public var happy: Happy
        }
        public struct Badge: Decodable, Sendable, Equatable {
            public var at, gap, dot: Double
        }
        public struct Host: Decodable, Sendable, Equatable {
            public var fontPx, radiusPx, iconCellPx, markHPx, markBottomPx: Double
            public var claudeColor, codexColor: RGB
            public var maxChars: Int
        }
        public struct Glint: Decodable, Sendable, Equatable {
            public var widthPx, tailDeg, whiteMix, periodS, fps: Double
        }
        public var fill: Double
        public var bodyColor, eyeColor: RGB
        public var eyeScale, hopScale, rimPx, rimDimGain: Double
        public var rimSteps: Int
        public var ringPoints: Int
        public var triangle: Triangle
        public var hop: Hop
        public var eyes: Eyes
        public var badge: Badge
        public var host: Host
        public var glint: Glint
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

    public struct NowPlaying: Decodable, Sendable, Equatable {
        public struct Colors: Decodable, Sendable, Equatable {
            public var track, arc, arcPaused, text, sub, meta, idle, placeholder, note: RGB
        }
        public var backdropDiskRadiusPx, ringRadiusPx, ringWidthPx: Double
        public var albumPx, albumDyPx, artistPx, dotPx: Double
        public var colors: Colors
        public var title, sub, meta, idle: Label
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
        public struct Scene: Decodable, Sendable, Equatable {
            public struct FPS: Decodable, Sendable, Equatable { public var rain, snow, other: Double }
            public struct Drops: Decodable, Sendable, Equatable { public var rain, storm, snow: [Int] }
            public struct DropAlpha: Decodable, Sendable, Equatable {
                public var rain: [Double]
                public var snow: Double
            }
            public struct Rain: Decodable, Sendable, Equatable {
                public var top, speed, speedSpread, respawnBand: Double
                public var x, slant: [Double]
            }
            public struct Snow: Decodable, Sendable, Equatable {
                public var x, amp, freq, speed: [Double]
                public var largeShare: Double
            }
            public struct Stars: Decodable, Sendable, Equatable {
                public var x, y, twinkleS: [Double]
                public var jitter, alpha, twinkleAlpha, firstTwinkleS: Double
            }
            public struct Bolt: Decodable, Sendable, Equatable {
                public var firstS, gapS, secondS, x: [Double]
                public var onS, doubleChance, y: Double
            }
            public struct Drift: Decodable, Sendable, Equatable {
                public var x, y, amp, periodS, phase, alpha: Double
            }
            public struct Fog: Decodable, Sendable, Equatable {
                public var x, y, step: Double
                public var amp, periodS, phase, alpha: [Double]
            }
            public struct Layout: Decodable, Sendable, Equatable {
                public var clearSun, nightMoon, partlyMoon, partlySun: [Double]
                public var partlyCloud, overcastBack, overcastFront, precipCloud: Drift
                public var fog: Fog
            }
            public var fps: FPS
            public var maxStepS: Double
            public var sprites: [String: [Double]]
            public var rayFrames: Int
            public var rayStepsPerS: Double
            public var drops: Drops
            public var dropAlpha: DropAlpha
            public var rain: Rain
            public var snow: Snow
            public var stars: Stars
            public var bolt: Bolt
            public var layout: Layout
        }
        public var sky: Sky
        public var temp: Temp
        public var maxAgeS: Double
        public var colors: Colors
        public var scene: Scene
    }

    public var version: Int
    public var screen: Screen
    public var font: Font
    public var moodColors: MoodColors
    public var bot: Bot
    public var pomodoro: Pomodoro
    public var weather: Weather
    public var nowplaying: NowPlaying

    public static var fontURL: URL? {
        Bundle.module.url(forResource: standard.font.file, withExtension: nil)
    }

    public static let standard: KnobTheme = {
        do {
            return try load()
        } catch {
            preconditionFailure("knob-theme.json: \(error)")
        }
    }()

    static var jsonURL: URL? { Bundle.module.url(forResource: "knob-theme", withExtension: "json") }

    public static func load() throws -> KnobTheme {
        guard let url = jsonURL else {
            throw CocoaError(.fileNoSuchFile)
        }
        return try decode(Data(contentsOf: url))
    }

    public static func decode(_ data: Data) throws -> KnobTheme {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return try d.decode(KnobTheme.self, from: data)
    }

    public func moodColor(_ mood: BotMood) -> RGB {
        switch mood {
        case .idle, .sleepy: moodColors.idle
        case .working: moodColors.working
        case .waiting: moodColors.waiting
        case .error: moodColors.error
        case .done: moodColors.done
        }
    }

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

    public func scaled(_ gain: Double) -> RGB {
        func s(_ v: UInt8) -> UInt8 { UInt8(min(255, max(0, Double(v) * gain))) }
        return RGB(r: s(r), g: s(g), b: s(b))
    }
}
