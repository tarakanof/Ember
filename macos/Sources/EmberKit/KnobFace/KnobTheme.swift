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
                public var width, widen, height, angleDeg: Double
            }
            public struct Happy: Decodable, Sendable, Equatable {
                public var width: Double
                public var height: [Double]
                public var lift, stroke: Double
                public var points: Int
            }
            public var restX, reach, reachTriangle, dropTriangle, dropSlump, limit, limitTriangle: Double
            public var foreshorten, straight, leanAt, sep, sepRound, rise, minHalf: Double
            public var lidKeep, lidDroop, blinkHide: Double
            public var dash, angry, round: Capsule
            public var happy: Happy
        }
        public struct Badge: Decodable, Sendable, Equatable {
            public var at, gap, dot: Double
        }
        public struct Host: Decodable, Sendable, Equatable {
            public var fontPx, radiusPx, iconCellPx, markHPx, markBottomPx: Double
            public var subFontPx, subRadiusPx, markBottomSubPx, canvasWPx, canvasHPx, canvasYPx: Double
            public var claudeColor, codexColor: RGB
            public var maxChars: Int

            public func markBottom(hostLine: Bool) -> Double { hostLine ? markBottomSubPx : markBottomPx }
        }
        public struct Glint: Decodable, Sendable, Equatable {
            public var widthPx, tailDeg, whiteMix, periodS, fps: Double
        }
        public struct ErrorFX: Decodable, Sendable, Equatable {
            public var gazeViewerBelow, gazeDownBelow: Double
            public var shakeAtS, shakeS, shakeCycles, shakeAmp: Double
            public var huffS, huffDip, huffLid, huffMedianS, huffSigma: Double
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
        public var error: ErrorFX
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
        public struct Scale: Decodable, Sendable, Equatable {
            public var loC, hiC: Double
            public var stopsC: [Double]
            public var colors: [RGB]
        }
        public struct Ring: Decodable, Sendable, Equatable {
            public var radiusPx, widthPx, fromDeg, sweepDeg: Double
            public var trackColor: RGB
        }
        public struct Marker: Decodable, Sendable, Equatable {
            public var discRadiusPx, ringRadiusPx, ringWidthPx: Double
            public var color: RGB
        }
        public struct Thermo: Decodable, Sendable, Equatable {
            public struct MercuryTop: Decodable, Sendable, Equatable {
                public var refC, refPx, risePx, perC, minPx, maxPx: Double
            }
            public var dyPx, headYPx, headRadiusPx, tubeMidYPx, tubeHalfHPx, tubeHalfWPx: Double
            public var bulbYPx, bulbRadiusPx, strokePx: Double
            public var strokeColor: RGB
            public var mercuryBottomPx, mercuryHalfWPx, mercuryBulbRadiusPx: Double
            public var mercuryTop: MercuryTop
            public var ticksPx, tickXPx: [Double]
            public var tickWidthPx: Double
            public var tickColor: RGB
        }
        public struct Value: Decodable, Sendable, Equatable {
            public var fontPx, yPx, letterSpacePx: Double
        }
        public struct Ends: Decodable, Sendable, Equatable {
            public var fontPx, xPx, yPx: Double
        }
        public struct Sub: Decodable, Sendable, Equatable {
            public var fontPx, yPx: Double
            public var color: RGB
        }
        public struct Cloud: Decodable, Sendable, Equatable {
            public var maxPct: Int
            public var edgesPct: [Int]
            public var hystPct, clearPct, partlyPct, otherPct: Int
        }
        public struct UV: Decodable, Sendable, Equatable {
            public var max, adviceFrom: Int
            public var levelMax: [Int]
            public var colors: [RGB]
        }
        public struct Dots: Decodable, Sendable, Equatable {
            public var radiusPx, pitchPx, dotRadiusPx: Double
            public var onColor, offColor: RGB
            public var faces: Int
        }
        public struct Unit: Decodable, Sendable, Equatable {
            public var fontPx, gapPx: Double
        }
        public struct Rain: Decodable, Sendable, Equatable {
            public var colors: [RGB]
            public var ringFrom, snowColor, boltColor, umbrellaColor, endColor: RGB
        }
        public struct Cast: Decodable, Sendable, Equatable {
            public var color, dryColor, cellColor: RGB
            public var spanS, stepS: Int
            public var labelFontPx, labelGapPx: Double
        }
        public struct Wind: Decodable, Sendable, Equatable {
            public var bandsKmh: [Int]
            public var colors: [RGB]
            public var gaugeKmh, maxKmh: Int
            public var edgesKmh: [Int]
            public var hystKmh: Int
            public var darkGain: Double
            public var poleColor, unitColor, streakColor, leafColor: RGB
        }
        public struct Days: Decodable, Sendable, Equatable {
            public var rowYPx, pitchPx, dayWPx, iconPx, tempWPx, barWPx, barHPx, gapPx, popDyPx: Int
            public var loOpacity: Int
            public var subYPx, ringWidthPx: Double
            public var trackColor, nameColor, popColor: RGB
            public var nameFontPx, popFontPx: Double
        }
        public struct Sun: Decodable, Sendable, Equatable {
            public var valueColor, goldColor, horizonColor, unitColor, subColor: RGB
            public var blueOut, blueIn, mauve, duskPink, dayEdge, dayNoon: RGB
            public var blendS, spanMaxS, rollDays, rollGraceS, countMaxS: Int
            public var cxPx, cyPx, radiusPx, clipYPx, horizonYPx: Double
        }
        public struct Air: Decodable, Sendable, Equatable {
            public var stops: [Int]
            public var stopColors: [RGB]
            public var bandMax: [Int]
            public var bandColors: [RGB]
            public var gauge, max, poor, hyst: Int
            public var unitColor: RGB
        }
        public var maxAgeS: Double
        public var stillColor: RGB
        public var scale: Scale
        public var ring: Ring
        public var marker: Marker
        public var thermo: Thermo
        public var value: Value
        public var ends: Ends
        public var sub: Sub
        public var cloud: Cloud
        public var uv: UV
        public var dots: Dots
        public var unit: Unit
        public var rain: Rain
        public var cast: Cast
        public var wind: Wind
        public var days: Days
        public var sun: Sun
        public var air: Air
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
        let h = bot.hop, e = bot.error
        let fx = BotBehavior.ErrorFX(gazeViewerBelow: e.gazeViewerBelow, gazeDownBelow: e.gazeDownBelow,
                                     shakeAt: e.shakeAtS, shakeLength: e.shakeS, shakeCycles: e.shakeCycles,
                                     shakeAmp: e.shakeAmp, huffLength: e.huffS, huffDip: e.huffDip,
                                     huffLid: e.huffLid, huffMedian: e.huffMedianS, huffSigma: e.huffSigma)
        return BotBehavior.Tuning(sleepAfter: BotBehavior.sleepAfter, hopLength: h.durationS, hopSquash: h.squash,
                                  hopIntervalMedian: h.intervalMedianS, hopIntervalSigma: h.intervalSigma,
                                  hopIntervalRange: h.intervalMinS...h.intervalMaxS, blinkHide: bot.eyes.blinkHide,
                                  errorFX: fx)
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
