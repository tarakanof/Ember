import Foundation
import Testing
@testable import EmberKit

private indirect enum CInit: Equatable, CustomStringConvertible {
    case num(Double)
    case word(String)
    case list([CInit])

    var description: String {
        switch self {
        case .num(let v): v == v.rounded() ? String(Int(v)) : String(v)
        case .word(let w): w
        case .list(let l): "{" + l.map(\.description).joined(separator: ", ") + "}"
        }
    }

    var items: [CInit] { if case .list(let l) = self { l } else { [] } }
    var word: String { if case .word(let w) = self { w } else { "" } }
}

private func nums(_ v: [Double]) -> CInit { .list(v.map { .num($0) }) }
private func box(_ r: CGRect) -> CInit { nums([r.minX, r.minY, r.maxX, r.maxY]) }
private func pt(_ p: CGPoint) -> CInit { nums([p.x, p.y]) }
private func f(_ v: Float) -> Double { Double(String(v))! }

private struct CParseError: Error, CustomStringConvertible {
    let description: String
}

private struct CReader {
    let s: [Character]
    var i = 0

    func peek() throws -> Character {
        guard i < s.count else { throw CParseError(description: "initialiser ends early") }
        return s[i]
    }

    mutating func skip() throws {
        while i < s.count {
            if s[i].isWhitespace {
                i += 1
            } else if s[i] == "/", i + 1 < s.count, s[i + 1] == "/" {
                while i < s.count, s[i] != "\n" { i += 1 }
            } else if s[i] == "/", i + 1 < s.count, s[i + 1] == "*" {
                i += 2
                while i + 1 < s.count, !(s[i] == "*" && s[i + 1] == "/") { i += 1 }
                guard i + 1 < s.count else { throw CParseError(description: "unterminated comment") }
                i += 2
            } else { return }
        }
    }

    mutating func value() throws -> CInit {
        try skip()
        if try peek() == "{" {
            i += 1
            var out: [CInit] = []
            while true {
                try skip()
                if try peek() == "}" { i += 1; return .list(out) }
                out.append(try value())
                try skip()
                let c = try peek()
                if c == "," { i += 1 } else if c != "}" { throw CParseError(description: "expected , or } at \(String(s[i...].prefix(20)))") }
            }
        }
        var t = ""
        var depth = 0
        while true {
            let c = try peek()
            if depth == 0, ",}".contains(c) { break }
            if s[i] == "/", i + 1 < s.count, s[i + 1] == "/" || s[i + 1] == "*" { break }
            if s[i] == "(" { depth += 1 } else if s[i] == ")" { depth -= 1 }
            t.append(s[i])
            i += 1
        }
        t = t.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.hasPrefix("0x"), let v = Int(t.dropFirst(2), radix: 16) { return .num(Double(v)) }
        let bare = t.hasSuffix("f") && Double(t.dropLast()) != nil ? String(t.dropLast()) : t
        if let v = Double(bare) { return .num(v) }
        guard t.range(of: #"^[A-Za-z_]\w*(\([A-Za-z_]\w*\))?$"#, options: .regularExpression) != nil else {
            throw CParseError(description: "unsupported value \"\(t)\"")
        }
        return .word(t)
    }
}

private func cTable(_ src: String, _ decl: String) throws -> CInit {
    let re = try NSRegularExpression(pattern: NSRegularExpression.escapedPattern(for: decl) + #"\s*=\s*"#)
    guard let m = re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)) else { throw CParseError(description: "no \(decl)") }
    var r = CReader(s: Array(src[Range(m.range, in: src)!.upperBound...]))
    return try r.value()
}

private func cDefine(_ src: String, _ name: String) throws -> Double {
    let re = try NSRegularExpression(pattern: "^#define " + NSRegularExpression.escapedPattern(for: name) + #"[ \t]+(.*)$"#, options: [.anchorsMatchLines])
    guard let m = re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)) else { throw CParseError(description: "no #define \(name)") }
    var v = String(src[Range(m.range(at: 1), in: src)!])
    if let c = v.range(of: "//") { v = String(v[..<c.lowerBound]) }
    if let c = v.range(of: "/*") {
        guard v[c.upperBound...].hasSuffix("*/"), !v[c.upperBound...].dropLast(2).contains("*/") else {
            throw CParseError(description: "\(name): unsupported comment in \(v)")
        }
        v = String(v[..<c.lowerBound])
    }
    v = v.trimmingCharacters(in: .whitespaces)
    let num = try NSRegularExpression(pattern: #"^(\()?(-?\d+(?:\.\d+)?)f?(\))?$"#)
    guard let n = num.firstMatch(in: v, range: NSRange(v.startIndex..., in: v)),
          (n.range(at: 1).location == NSNotFound) == (n.range(at: 3).location == NSNotFound),
          let d = Double(String(v[Range(n.range(at: 2), in: v)!])) else {
        throw CParseError(description: "\(name): unsupported value \(v)")
    }
    return d
}

private func source(_ file: String) throws -> String {
    try String(contentsOf: try #require(cinderDir).appendingPathComponent("firmware/components/weather/" + file), encoding: .utf8)
}

private func table(_ file: String, _ decl: String) throws -> CInit { try cTable(try source(file), decl) }

private func define(_ file: String, _ name: String) throws -> Double { try cDefine(try source(file), name) }

private func countOK(_ count: CInit, _ array: CInit) -> Bool { !array.word.isEmpty && count == .word("N(\(array.word))") }

private func counted(_ count: CInit, _ array: CInit, _ what: String) -> Bool {
    let ok = countOK(count, array)
    if !ok { Issue.record("\(what): unsupported element count \(count) for \(array)") }
    return ok
}

private func entry(_ c: CInit, _ n: Int, _ what: String) -> [CInit]? {
    let v = c.items
    if v.count != n { Issue.record("\(what): \(v.count) fields, want \(n): \(c)") }
    return v.count == n ? v : nil
}

@Test func cParserRejectsWhatItCannotRead() throws {
    #expect(try cDefine("#define A 4\n", "A") == 4)
    #expect(try cDefine("#define A 0.16f // fraction\n", "A") == 0.16)
    #expect(try cDefine("#define A (195.0f) /* px */\n", "A") == 195)
    #expect(try cDefine("#define A -7\n#define AB 9\n", "AB") == 9)
    for bad in ["#define A 4 + 2\n", "#define A (2 * B)\n", "#define A 4)\n", "#define A 0x10\n", "#define A 4 /* x */ + 1\n", "#define A\n"] {
        #expect(throws: CParseError.self, "\(bad)") { try cDefine(bad, "A") }
    }
    #expect(try cTable("int T[] = {1, /* a */ 2, // b\n 3.5f};", "int T[]") == nums([1, 2, 3.5]))
    #expect(try cTable("int T[] = {{1, N(X)}, Y};", "int T[]") == .list([.list([.num(1), .word("N(X)")]), .word("Y")]))
    for bad in ["int T[] = {1, 2", "int T[] = {1, /* 2};", "int T[] = {1 2};", "int T[] = {1, , 2};", "int T[] = {{1} 2};", "int T[] = {N(X) - 1};", "int U[] = {1};"] {
        #expect(throws: CParseError.self, "\(bad)") { try cTable(bad, "int T[]") }
    }
    #expect(countOK(.word("N(LEFT)"), .word("LEFT")))
    #expect(!countOK(.num(9), .word("LEFT")) && !countOK(.word("N(RIGHT)"), .word("LEFT")) && !countOK(.word("N()"), .word("")))
}

private func code(_ file: String, _ pattern: String) throws {
    let src = try source(file)
    let re = try NSRegularExpression(pattern: pattern, options: [.dotMatchesLineSeparators])
    #expect(re.firstMatch(in: src, range: NSRange(src.startIndex..., in: src)) != nil, "\(file): no match for \(pattern)")
}

private let cinder = Testing.ConditionTrait.enabled(if: cinderDir != nil, "no cinder checkout next to this repo (set CINDER_DIR)")

@Test(cinder) func rainSheetsMatchCinder() throws {
    let sheets = try table("wx_rain.c", "static const wx_sheet_t SHEETS[WX_SHEETS]").items
    #expect(sheets.count == KnobRainFace.sheets.count)
    for (c, s) in zip(sheets, KnobRainFace.sheets) {
        guard let v = entry(c, 5, "rain sheet"), counted(v[3], v[4], "rain sheet") else { continue }
        #expect(Array(v.prefix(3)) == [box(s.box), .num(Double(s.tileFrames)), .num(Double(s.delayFrames))], "sheet \(c)")
        let drops = try table("wx_rain.c", "static const wx_drop_t \(v[4].word)[]")
        #expect(drops == .list(s.drops.map { nums([$0.x, $0.y, $0.len, $0.op, $0.slant, Double($0.tile), Double($0.level)]) }), "\(v[4].word)")
    }
    #expect(try define("include/wx_rain.h", "WX_SHEET_H") == Double(KnobRainFace.sheetH))
    try code("include/wx_rain.h", #"#define WX_SHEET_PERIOD \(2 \* WX_SHEET_H\)\n"#)
    #expect(try define("include/wx_paint.h", "WX_RAMP") == KnobRainFace.ramp)
    try code("wx_rain.c", #"static int offset\(int frames, int a\) \{ return \(int\)lround\(\(double\)WX_SHEET_PERIOD \* a / frames\) % WX_SHEET_PERIOD; \}"#)
    try code("wx_rain.c", #"int f = 2 \* SHEETS\[s\]\.tile_frames;\s*return r && r->kind == WX_PRECIP_SNOW \? 2 \* f : f;"#)
    try code("wx_rain.c", #"f->off\[s\] = offset\(frames, \(k \+ SHEETS\[s\]\.delay_frames\) % frames\);"#)
    try code("wx_rain.c", #"ops->strip_top\[s\]\[0\] = f->off\[s\] - WX_SHEET_PERIOD;\s*ops->strip_top\[s\]\[1\] = f->off\[s\];"#)
}

@Test(cinder) func noDripSplashOrBoltShowsAtFrameZero() throws {
    try code("wx_rain.c", #"float u = \(x - 233\.0f\) / 128\.0f;\s*float k = 1 - u \* u;\s*return 198\.0f - 103\.0f \* sqrtf\(k > 0 \? k : 0\);"#)
    let splashSheet = 2
    try code("wx_rain.c", #"const wx_drop_t \*d = &TOP\[SPLASH_DROP\[n\]\];\s*"#
        + #"\*at = \(wx_splash_at_t\)\{\(int\)d->x - 8, \(int\)lroundf\(wx_canopy_y\(d->x\) - 3\) - 9, "#
        + "\(splashSheet), " + #"SPLASH_DROP\[n\]\};"#)
    try code("wx_rain.c", #"float ty = d->y \+ \(d->tile \? WX_SHEET_H : 0\);\s*"#
        + #"float t = fmodf\(wx_canopy_y\(d->x\) - 2 - \(float\)s->box\.y0 - ty - d->len, \(float\)WX_SHEET_PERIOD\);\s*"#
        + #"if \(t < 0\) t \+= WX_SHEET_PERIOD;\s*int frames = 2 \* s->tile_frames;\s*"#
        + #"for \(int a = 0; a < frames; a\+\+\)\s*if \(\(float\)offset\(frames, a\) >= t\) return a;"#)
    try code("wx_rain.c", #"int a = \(k \+ SHEETS\[at\.sheet\]\.delay_frames\) % frames;\s*"#
        + #"int j = \(\(a - wx_splash_hit\(n\)\) % frames \+ frames\) % frames;\s*if \(j >= 3\) continue;"#)
    try code("wx_rain.c", #"int j = k % DRIP\[i\]\.period - DRIP\[i\]\.start;\s*if \(snow \|\| j < 0 \|\| j >= 7\) continue;"#)
    try code("wx_rain.c", #"for \(int i = 0; i < 6; i\+\+\) f->bolt \|= k % WX_STORM_FRAMES == FLASH\[i\];"#)
    let top = KnobRainFace.sheets[splashSheet], period = Float(2 * KnobRainFace.sheetH), frames = 2 * top.tileFrames
    func offset(_ a: Int) -> Int { Int((Double(period) * Double(a) / Double(frames)).rounded()) % Int(period) }
    for n in try table("wx_rain.c", "static const int SPLASH_DROP[WX_SPLASHES]").items {
        guard case .num(let idx) = n, Int(idx) < top.drops.count else { Issue.record("splash drop \(n)"); continue }
        let d = top.drops[Int(idx)], u = (Float(d.x) - 233) / 128, k = 1 - u * u
        let canopy = 198 - 103 * (k > 0 ? k : 0).squareRoot()
        var t = fmodf(canopy - 2 - Float(top.box.minY) - Float(d.y) - Float(d.tile * KnobRainFace.sheetH) - Float(d.len), period)
        if t < 0 { t += period }
        let hit = (0..<frames).first { Float(offset($0)) >= t } ?? 0
        let j = ((top.delayFrames % frames - hit) % frames + frames) % frames
        #expect(j >= 3, "splash on drop \(Int(idx)) shows at frame 0 (j \(j))")
    }
    for drip in try table("wx_rain.c", "} DRIP[WX_DRIPS]").items {
        guard case .list(let v) = drip, v.count == 2, case .num(let start) = v[1] else { Issue.record("drip \(drip)"); continue }
        #expect(0 - Int(start) < 0, "drip \(drip) shows at frame 0")
    }
    let flash = try table("wx_rain.c", "static const int FLASH[6]")
    let storm = try define("include/wx_rain.h", "WX_STORM_FRAMES")
    #expect(!flash.items.contains(.num(0)) && storm > 0, "a bolt at frame 0")
}

@Test(cinder) func castSheetsCloudsAndBreatheMatchCinder() throws {
    let sheets = try table("wx_cast.c", "static const wx_cast_sheet_t SHEETS[3][WX_CAST_SIDES]").items
    #expect(sheets.count == KnobCastFace.sheets.count)
    for (cv, sv) in zip(sheets, KnobCastFace.sheets) {
        #expect(cv.items.count == sv.count)
        for (c, s) in zip(cv.items, sv) {
            guard let v = entry(c, 8, "cast sheet"), counted(v[5], v[6], "cast sheet") else { continue }
            #expect(s.a.count == s.b.count, "both tiles hold n streaks")
            #expect(Array(v.prefix(5)) == [box(s.box), .num(Double(s.h)), .num(Double(s.tileFrames)), .num(Double(s.delayFrames)), .num(s.width)],
                    "sheet \(c)")
            for (name, streaks) in [(v[6].word, s.a), (v[7].word, s.b)] {
                let t = try table("wx_cast.c", "static const wx_streak_t \(name)[]")
                #expect(t == .list(streaks.map { nums([$0.x, $0.y, $0.len, $0.op, $0.slant]) }), "\(name)")
            }
        }
    }
    let clouds = try table("wx_paint.c", "static const cast_clouds_t CAST_CLOUDS[3][WX_CAST_SIDES]").items
    let toneNames = ["WX_TONE_WHITE", "WX_TONE_LIGHT", "WX_TONE_DARK"]
    #expect(clouds.count == KnobCastFace.clouds.count)
    for (cv, sv) in zip(clouds, KnobCastFace.clouds) {
        #expect(cv.items.count == sv.count)
        for (c, s) in zip(cv.items, sv) {
            let want: [CInit] = [.num(Double(s.clouds.count)), .word(toneNames[s.tone]), .num(s.fadeFrom),
                                 .list(s.clouds.map { cl in
                                     .list([.num(cl.back ? 1 : 0), .num(cl.base), .num(Double(cl.circles.count)), .list(cl.circles.map { nums([$0.x, $0.y, $0.r]) })])
                                 })]
            #expect(c == .list(want), "clouds \(c)")
        }
    }
    let tones = try table("wx_paint.c", "static const uint32_t TONES[4][3]").items
    func packed(_ c: RGB) -> CInit { .num(Double(Int(c.r) << 16 | Int(c.g) << 8 | Int(c.b))) }
    #expect(Array(tones.prefix(3)) == KnobCastFace.tones.map { CInit.list([packed($0.front), packed($0.back), packed($0.line)]) })
    let breathe = try table("wx_cast.c", "static const double P[3][WX_CAST_SIDES][2]")
    #expect(breathe == .list(KnobCastFace.breathe.map { .list($0.map { nums([$0.period, $0.delay]) }) }))
    #expect(try define("include/wx_cast.h", "WX_CAST_FPS") == Double(KnobCastFace.fps))
    #expect(try define("include/wx_cast.h", "WX_CAST_RAMP") == KnobCastFace.ramp)
    #expect(try define("include/wx_cast.h", "WX_CAST_BREATHE_PX") == Double(KnobCastFace.breathePx))
    #expect(try define("wx_paint.c", "RIM_R") == KnobCastFace.rimR)
    try code("wx_cast.c", #"int frames = 2 \* s->tile_frames \* \(snow \? 2 : 1\), period = 2 \* s->h;\s*if \(k < 0\) k = 0;\s*"#
        + #"int a = \(k \+ s->delay_frames\) % frames;\s*return \(int\)lround\(\(double\)period \* a / frames\) % period;"#)
    try code("wx_cast.c", #"double p = fmod\(t - delay, period\);\s*if \(p < 0\) p \+= period;\s*"#
        + #"double half = period / 2, u = p < half \? p / half : \(p - half\) / half;\s*double q = floor\(u \* n \+ 1e-9\) / n;\s*return p < half \? q : 1 - q;"#)
    try code("wx_cast.c", #"return \(int\)lround\(-WX_CAST_BREATHE_PX \+ 2\.0 \* WX_CAST_BREATHE_PX \* f\);"#)
    try code("wx_cast.c", #"if \(variant != 3\) return 255;\s*double f = tri_steps\(\(double\)\(k < 0 \? 0 : k\) / WX_CAST_FPS, 3\.2, 0, WX_CAST_GLOOM_STEPS\);"#)
}

@Test(cinder) func windGeometryAndFrameMatchCinder() throws {
    let geo = try table("wx_wind.c", "static const wx_wind_geo_t GEO[WX_WIND_LEVELS]").items
    #expect(geo.count == KnobWindFace.geo.count)
    for (c, g) in zip(geo, KnobWindFace.geo) {
        guard let v = entry(c, 10, "wind geo") else { continue }
        let want: [CInit] = [pt(g.mount), .num(g.droop), .num(Double(g.poseFrames)), .num(g.ang), .num(g.stretch),
                             .list(g.quads.map { .list($0.map(pt)) }), .num(Double(g.rows.count)), .num(Double(g.leaves.count)),
                             .list(g.rows.map { nums([f($0.dy), f($0.x0), f($0.len), f($0.period), f($0.phase), Double($0.steps)]) })]
        #expect(Array(v.prefix(9)) == want, "geo \(c)")
        #expect(Array(v[9].items.prefix(g.leaves.count)) == g.leaves.map { nums([f($0.dy), f($0.period), f($0.phase)]) }, "leaves \(c)")
    }
    #expect(try define("include/wx_wind.h", "WX_WIND_FPS") == Double(KnobWindFace.fps))
    #expect(try define("include/wx_wind.h", "WX_LEAF_BOX") == Double(KnobWindFace.leafBox))
    #expect(try define("include/wx_wind.h", "WX_LEAF_ROTS") == Double(KnobWindFace.leafRots))
    #expect(try define("include/wx_wind.h", "WX_WIND_ARM") == KnobWindFace.arm)
    #expect(try define("include/wx_wind.h", "WX_WIND_POLE_TOP") == KnobWindFace.poleTop)
    #expect(try define("include/wx_wind.h", "WX_WIND_BASE_Y") == KnobWindFace.baseY)
    #expect(try table("wx_wind.c", "static const int SEQ[4]") == nums(KnobWindFace.poseSeq.map(Double.init)))
    try code("wx_wind.c", #"if \(pose == 0\) \{\s*a = -g->ang;\s*sx = 1 - g->stretch;\s*sy = 1\.02f;\s*\} else if \(pose == 2\) \{\s*"#
        + #"a = g->ang;\s*sx = 1 \+ g->stretch;\s*sy = 0\.96f;"#)
    try code("wx_wind.c", #"f->streak_dx\[r\] = \(int\)lround\(first \? -24 \+ 40 \* u : 16 \+ 40 \* u\);\s*"#
        + #"f->streak_opa\[r\] = \(int\)lround\(255 \* 0\.75 \* \(first \? u : 1 - u\)\);"#)
    try code("wx_wind.c", #"tx = 70 \* u;\s*ty = -10 \* u;\s*rot = 160 \* u;.*?tx = 70 \+ 70 \* u;\s*ty = -10 \+ 16 \* u;\s*rot = 160 \+ 170 \* u;"#
        + #".*?if \(p < 0\.12\) op = stepped\(p / 0\.12, n\);\s*else if \(p < 0\.88\) op = 1;\s*else op = 1 - stepped\(\(p - 0\.88\) / 0\.12, n\);"#
        + #"\s*double cx = g->mount\.x - 6 \+ 8 \+ tx, cy = g->mount\.y \+ l->dy \+ ty;"#)
    try code("wx_wind.c", #"f->pose = 1;\s*if \(k < 0\) return;"#)
    try code("wx_paint.c", #"if \(12 \* t \+ 12 \* t \* t - 8 \* t \* t \* t < u\) lo = t;.*?hw\[i\] = 15 \* t \* \(1 - t\);"#)
    try code("wx_paint.c", #"float u = dx \* c \+ dy \* s \+ 8, v = -dx \* s \+ dy \* c;"#)
}

@Test func restFrameIsCindersFrameZero() {
    #expect(KnobRainFace.sheets.map { KnobRainFace.sheetOffset($0, snow: false, k: 0) } == [45, 94, 0])
    #expect(KnobRainFace.sheets.map { KnobRainFace.sheetOffset($0, snow: true, k: 0) } == [23, 47, 0])
    #expect(KnobCastFace.sheets.map { $0.map { KnobCastFace.sheetOffset($0, snow: false, k: 0) } } == [[0], [17, 31], [15, 39]])
    #expect(KnobCastFace.sheets.map { $0.map { KnobCastFace.sheetOffset($0, snow: true, k: 0) } } == [[0], [8, 16], [8, 19]])
    #expect([(1, 0), (2, 0), (2, 1), (3, 0), (3, 1)].map { KnobCastFace.breathe(variant: $0.0, side: $0.1, k: 0) } == [-4, -4, 3, -4, 0])
    typealias F = KnobWindFace.Frame
    #expect(KnobWindFace.frame(level: 0, k: 0) == F(pose: 0, streaks: [.init(dx: -24, opa: 0)]))
    #expect(KnobWindFace.frame(level: 1, k: 0) == F(pose: 0, streaks: [.init(dx: -24, opa: 0), .init(dx: -1, opa: 112), .init(dx: 29, opa: 128)]))
    #expect(KnobWindFace.frame(level: 2, k: 0) == F(pose: 0, streaks: [.init(dx: -24, opa: 0), .init(dx: -4, opa: 96), .init(dx: 16, opa: 191),
                                                                      .init(dx: 36, opa: 96)],
                                                    leaves: [.init(x: 183, y: 81, rot: 0, opa: 0), .init(x: 242, y: 147, rot: 9, opa: 255)]))
    #expect(KnobWindFace.frame(level: 2, k: nil) == F(), "no data: still pose B, no streaks or leaves")
    let q = KnobWindFace.sockQuads(level: 1, pose: 0)
    #expect(abs(q[0][0].x - 210.712) < 0.01 && abs(q[0][0].y - 79.561) < 0.01 && abs(q[4][2].x - 302.114) < 0.01 && abs(q[4][2].y - 163.547) < 0.01)
}

private let face = "../../main/temp_face.c"

@Test(cinder) func arrivalClockMatchesCinder() throws {
    #expect(try define("include/wx_temp.h", "WX_ARRIVE_S") == KnobArrive.seconds)
    #expect(try define("include/wx_temp.h", "WX_COUNT_MAX") == Double(KnobArrive.countMax))
    #expect(try define(face, "ARRIVE_FPS") == KnobArrive.fps && define(face, "FADE_FPS") == KnobArrive.fadeFps)
    try code("wx_temp.c", #"double wx_arrive\(double t\)\s*\{\s*if \(!\(t > 0\)\) return 0;\s*if \(t >= WX_ARRIVE_S\) return 1;\s*"#
        + #"double p = 1 - t / WX_ARRIVE_S;\s*return 1 - p \* p \* p;"#)
    try code("wx_temp.c", #"int a = v < 0 \? -v : v;\s*return a <= WX_COUNT_MAX \? a : 23;"#)
    try code("wx_temp.c", #"if \(k <= 0 \|\| n == 0\) return 0;\s*if \(k >= n\) return v;\s*if \(n == \(v < 0 \? -v : v\)\) return v < 0 \? -k : k;\s*"#
        + #"return \(int\)lround\(\(double\)k \* v / n\);"#)
    try code("wx_temp.c", #"if \(!\(t > 0\)\) return 0;\s*if \(t >= WX_ARRIVE_S\) return n;\s*double p = 1 - t / WX_ARRIVE_S;\s*"#
        + #"int k = \(int\)floor\(n \* \(1 - p \* p \* p\) \+ 1e-9\);\s*return k > n \? n : k;"#)
    try code(face, #"double ta = floor\(s_t \* ARRIVE_FPS\) / ARRIVE_FPS;.*?double tf = floor\(s_t \* FADE_FPS\) / FADE_FPS;\s*"#
        + #"int sprite_opa = \(int\)lround\(fo \* wx_arrive\(tf\)\);"#)
}

@Test(cinder) func rainFrameTablesAndSpritesMatchCinder() throws {
    #expect(try define("include/wx_rain.h", "WX_RAIN_FPS") == KnobWeatherMotion.fps)
    #expect(try define("include/wx_rain.h", "WX_STORM_FRAMES") == Double(KnobRainFace.stormFrames))
    #expect(try table("wx_rain.c", "static const int FLASH[6]") == nums(KnobRainFace.flash.map(Double.init)))
    #expect(try table("wx_rain.c", "static const int SPLASH_DROP[WX_SPLASHES]") == nums(KnobRainFace.splashDrop.map(Double.init)))
    #expect(try table("wx_rain.c", "} DRIP[WX_DRIPS]") == .list(KnobRainFace.drips.map { nums([Double($0.period), Double($0.start)]) }))
    #expect(try table("wx_rain.c", "} DRIP_SEQ[7]") == .list(KnobRainFace.dripSeq.map { nums([Double($0.pose), Double($0.dy), Double($0.opa)]) }))
    #expect(try table("wx_rain.c", "} SPLASH_SEQ[3]") == .list(KnobRainFace.splashSeq.map { nums([Double($0.dy), Double($0.opa)]) }))
    #expect(try table("wx_paint.c", "static const float DRIP_X[WX_DRIPS]") == nums(KnobRainFace.dripX))
    #expect(try table("wx_paint.c", "static const float SCALE[WX_DRIP_POSES]") == nums(KnobRainFace.dripScale))
    #expect(try define("include/wx_paint.h", "WX_BOLT_X") == KnobRainFace.boltAt.x && define("include/wx_paint.h", "WX_BOLT_Y") == KnobRainFace.boltAt.y)
    #expect(try table("wx_paint.c", "static const float P[7][2]") == .list(KnobRainFace.bolt.map(pt)))
    try code(face, "#define BOLT_RGB 0x" + KnobRainFace.boltColor.hex.dropFirst() + #"\n"#)
    try code("wx_paint.c", #"const float k = 0\.8f, o = 1\.0f;.*?cov\(\(in \? -d : d\) - 0\.8f\)"#)
    try code("wx_paint.c", #"static float drip_hw\(float t\) \{ return 8\.0f \* t \* \(1 - t\); \}"#)
    try code("wx_paint.c", #"const float ox = WX_DRIP_W / 2\.0f, oy = 203\.0f - WX_DRIP_Y;.*?"#
        + #"float py = \(\(float\)y \+ \(sy \+ 0\.5f\) / 4 - oy\) / sc \+ 203\.0f;\s*in\[sy\] = !\(py < 199 \|\| py > 208\);.*?"#
        + #"float yt = 199 \* u \* u \+ 2 \* u \* t \* 205 \+ 208 \* t \* t;.*?"#
        + #"if \(fabsf\(\(\(float\)x \+ \(sx \+ 0\.5f\) / 4 - ox\) / sc\) <= hw\[sy\]\) hit\+\+;"#)
    try code("wx_paint.c", #"float x = d->x, y = wx_canopy_y\(d->x\) - 3;.*?"#
        + #"float dd = fminf\(seg_d\(px, py, x, y, x - 5, y - 6\), seg_d\(px, py, x, y, x \+ 5, y - 6\)\);\s*put_a\(&a8\[r \* WX_SPLASH_W \+ c\], cov\(dd - 1\.5f\)\);"#)
    try code("wx_rain.c", #"f->drip_pose\[i\] = DRIP_SEQ\[j\]\.pose;\s*f->drip_dy\[i\] = DRIP_SEQ\[j\]\.dy;\s*f->drip_opa\[i\] = DRIP_SEQ\[j\]\.opa;"#)
    try code("wx_rain.c", #"if \(!wx_drop_shown\(&SHEETS\[at\.sheet\]\.drop\[at\.drop\], r\)\) continue;\s*int frames = wx_sheet_frames\(r, at\.sheet\);"#)
    try code("wx_rain.c", #"f->splash_dy\[n\] = SPLASH_SEQ\[j\]\.dy;\s*f->splash_opa\[n\] = SPLASH_SEQ\[j\]\.opa;"#)
    try code("wx_rain.c", #"if \(r->kind == WX_PRECIP_STORM\)\s*for"#)
    try code("wx_rain.c", #"ops->drip_top\[i\] = WX_DRIP_Y \+ f->drip_dy\[i\] - SHEETS\[i\]\.box\.y0;\s*ops->drip_alpha\[i\] = f->drip_opa\[i\] \* opa / 255;"#)
    try code("wx_rain.c", #"ops->splash_top\[n\] = at\.y \+ f->splash_dy\[n\] - SHEETS\[at\.sheet\]\.box\.y0;\s*ops->splash_alpha\[n\] = f->splash_opa\[n\] \* opa / 255;"#)
    try code(face, #"wx_rain_frame\(r, \(int\)floor\(s_t \* WX_RAIN_FPS\), &f\);"#)
    try code(face, #"set_rain\(r, sprite_opa, fo == 255 && !r->dry\);"#)
    try code(face, #"if \(op\.opa\) lv_obj_set_style_image_opa\(s_bolt, \(lv_opa_t\)op\.layer_opa, 0\);"#)
}

@Test(cinder) func castArrivalAndGloomMatchCinder() throws {
    #expect(try define("include/wx_cast.h", "WX_CAST_GATE_S") == KnobCastFace.gateS)
    #expect(try define("include/wx_cast.h", "WX_CAST_GATE_END_S") == KnobCastFace.gateEndS)
    #expect(try define("include/wx_cast.h", "WX_CAST_SLIDE_S") == KnobCastFace.slideS)
    #expect(try define("include/wx_cast.h", "WX_CAST_LABEL_END_S") == KnobCastFace.labelEndS)
    #expect(try define("include/wx_cast.h", "WX_CAST_GLOOM_STEPS") == Double(KnobCastFace.gloomSteps))
    try code("wx_cast.c", #"double f = t <= t0 \? 0 : t >= t1 \? 1 : \(t - t0\) / \(t1 - t0\);\s*return \(int\)lround\(255 \* \(up \? f : 1 - f\)\);"#)
    try code("wx_cast.c", #"int wx_cast_gate_opa\(double t\) \{ return ramp\(t, WX_CAST_GATE_S, WX_CAST_GATE_END_S, true\); \}"#)
    try code("wx_cast.c", #"int wx_cast_label_opa\(double t\) \{ return ramp\(t, WX_CAST_SLIDE_S, WX_CAST_LABEL_END_S, true\); \}"#)
    try code("wx_cast.c", #"return \(int\)lround\(255 \* \(1 - 0\.3 \* f\)\);"#)
    try code(face, #"int k = \(int\)floor\(s_t \* WX_CAST_FPS\);\s*int gate = wx_cast_gate_opa\(\(double\)k / WX_CAST_FPS\);\s*"#
        + #"bool rain = fo == 255 && gate > 0 && !c->dry;"#)
    try code(face, #"int dx = wx_cast_breathe\(c->variant, s, k\), opa = sprite_opa \* wx_cast_gloom\(c->variant, k\) / 255;"#)
    try code(face, #"int off = wx_cast_sheet_off\(sh, c->snow, k\);"#)
    try code(face, #"double tl = floor\(s_t \* FADE_FPS\) / FADE_FPS;\s*set_label\(c, wx_cast_label_opa\(tl\) \* fo / 255\);"#)
}

@Test(cinder) func windArrivalMatchesCinder() throws {
    try code(face, #"wx_wind_frame\(w->level, w->have && fo == 255 \? \(int\)floor\(s_t \* WX_WIND_FPS\) : -1, &f\);"#)
    try code(face, #"set_wind\(w, rgb, fo, sprite_opa\);"#)
    try code(face, #"lv_obj_set_style_image_opa\(s_wsock, \(lv_opa_t\)opa, 0\);\s*lv_obj_set_style_image_opa\(s_wdark, \(lv_opa_t\)opa, 0\);"#)
    try code(face, #"int o = f\.streak_opa\[r\] \* opa / 255;"#)
    try code(face, #"int o = f\.leaf_opa\[i\] \* opa / 255;"#)
}

@Test(cinder) func sunDaysAndAirFramesMatchCinder() throws {
    #expect(try define("include/wx_sun.h", "WX_SUN_RISE_PX") == Double(KnobSunFace.risePx))
    #expect(try define("include/wx_sun.h", "WX_SUN_FPS") == KnobSunFace.fps)
    #expect(try define("include/wx_sun.h", "WX_SUN_RAYS_ON_S") == KnobSunFace.raysOnS && define("include/wx_sun.h", "WX_SUN_RAYS_IN_S") == KnobSunFace.raysInS)
    try code("wx_sun.c", #"f->dy = \(int\)lround\(WX_SUN_RISE_PX \* \(1 - wx_arrive\(t\)\)\);\s*"#
        + #"double tf = t > 0 \? floor\(t \* WX_SUN_FPS \+ 1e-9\) / WX_SUN_FPS : 0;\s*"#
        + #"double p = \(tf - WX_SUN_RAYS_ON_S\) / \(WX_SUN_RAYS_IN_S - WX_SUN_RAYS_ON_S\);\s*"#
        + #"f->rays_opa = !\(p > 0\) \? 0 : p >= 1 \? 255 : \(int\)lround\(255 \* p\);"#)
    try code(face, #"wx_sun_frame\(ta, &f\);"#)
    try code(face, #"lv_obj_set_y\(s_ssun, f\.dy\);"#)
    try code(face, #"int rays = hero \? f\.rays_opa \* fo / 255 : 0;"#)
    #expect(try define("include/wx_days.h", "WX_DAYS_SLIDE") == Double(KnobDaysFace.slidePx))
    #expect(try define("include/wx_days.h", "WX_DAYS_STAGGER_S") == KnobDaysFace.staggerS && define("include/wx_days.h", "WX_DAYS_IN_S") == KnobDaysFace.inS)
    try code("wx_days.c", #"double p = \(t - i \* WX_DAYS_STAGGER_S\) / WX_DAYS_IN_S;\s*"#
        + #"double u = !\(p > 0\) \? 0 : p >= 1 \? 1 : 1 - \(1 - p\) \* \(1 - p\) \* \(1 - p\);\s*"#
        + #"\*opa = \(int\)lround\(255 \* u\);\s*\*dy = \(int\)lround\(WX_DAYS_SLIDE \* \(1 - u\)\);"#)
    try code("wx_days.c", #"f->lo\[i\] = wx_count_value\(d->row\[i\]\.lo, wx_count_step\(wx_count_n\(d->row\[i\]\.lo\), t\)\);\s*"#
        + #"f->hi\[i\] = wx_count_value\(d->row\[i\]\.hi, wx_count_step\(wx_count_n\(d->row\[i\]\.hi\), t\)\);"#)
    try code(face, #"wx_days_frame\(d, ta, &f\);"#)
    try code(face, #"int opa = f\.opa\[i\] \* fo / 255, dy = f\.dy\[i\], lo = f\.lo\[i\], hi = f\.hi\[i\];\s*if \(opa != s_shown\.day_opa\[i\]\) \{\s*"#
        + #"s_shown\.day_opa\[i\] = opa;\s*lv_obj_set_style_opa\(s_drow\[i\], \(lv_opa_t\)opa, 0\);"#)
    #expect(try define("include/wx_air.h", "WX_AIR_FPS") == KnobWeatherMotion.fps)
    #expect(try define("include/wx_wind.h", "WX_WIND_FPS") == KnobWeatherMotion.fps && define("include/wx_cast.h", "WX_CAST_FPS") == KnobWeatherMotion.fps)
    #expect(try define("wx_air.c", "PETAL_FRAMES") == Double(KnobAirFace.petalFrames) && define("wx_air.c", "PETAL_PHASE") == Double(KnobAirFace.petalPhase))
    #expect(try define("include/wx_air.h", "WX_PETAL_X") == KnobAirFace.petalAt.x && define("include/wx_air.h", "WX_PETAL_Y") == KnobAirFace.petalAt.y)
    #expect(try define("include/wx_air.h", "WX_PETAL_BOX") == Double(KnobAirFace.petalBox))
    try code("wx_air.c", #"if \(3 \* v \* v \* u \* 0\.42 \+ 3 \* v \* u \* u \* 0\.58 \+ u \* u \* u < p\) lo = u;.*?return 3 \* \(1 - u\) \* u \* u \+ u \* u \* u;"#)
    try code("wx_air.c", #"double t = \(double\)\(\(k \+ PETAL_PHASE\) % PETAL_FRAMES\) / PETAL_FRAMES;\s*"#
        + #"double u = t < 0\.62 \? ease\(t / 0\.62\) : 1;\s*"#
        + #"double op = t < 0\.08 \? ease\(t / 0\.08\) : t < 0\.9 \? 1 : 1 - ease\(\(t - 0\.9\) / 0\.1\);\s*"#
        + #"p->x = \(int\)lround\(WX_PETAL_X \+ 14 \* \(1 - u\)\) - WX_PETAL_BOX / 2;\s*"#
        + #"p->y = \(int\)lround\(WX_PETAL_Y - 58 \* \(1 - u\)\) - WX_PETAL_BOX / 2;\s*"#
        + #"p->rot = \(int\)lround\(5 \* u\);\s*p->opa = \(int\)lround\(255 \* op\);"#)
    try code("wx_paint.c", #"ink_t pe = \{\.fill = PETAL_RGB, \.line = PETAL_LINE, \.has_fill = true, \.has_line = true, \.hw = 0\.75f, \.alpha = 1\};\s*"#
        + #"petal\(im, NULL, 0, WX_PETAL_X, WX_PETAL_Y, 4\.5f, 8, 86\.0f - 50\.0f \+ 10\.0f \* \(float\)rot, WX_PETAL_X, WX_PETAL_Y, &pe\);"#)
    try code(face, #"wx_air_petal\(aq->wilted && fo == 255 \? \(int\)floor\(s_t \* WX_AIR_FPS\) : -1, &p\);"#)
}

@Test(cinder) func arrivalCountsMarkersAndSubFadeMatchCinder() throws {
    #expect(try define(face, "SUB_FADE_S") == KnobArrive.subFadeS)
    try code(face, #"double sf = \(s_t - WX_ARRIVE_S\) / SUB_FADE_S;\s*"#
        + #"int sub_opa = \(sf <= 0 \? 0 : sf >= 1 \? 255 : 85 \* \(1 \+ \(int\)\(sf \* 3\)\)\) \* fo / 255;"#)
    try code(face, #"if \(s_t < WX_ARRIVE_S \+ SUB_FADE_S && tick == s_tick"#)
    try code(face, #"double ta = floor\(s_t \* ARRIVE_FPS\) / ARRIVE_FPS;\s*double a = wx_arrive\(ta\);"#)
    try code(face, #"int v = wx_count_value\(aq->aqi, wx_count_step\(wx_count_n\(aq->aqi\), ta\)\);.*?set_value\(txt, wx_air_rgb\(v\)\);.*?"#
        + #"set_marker\(aq->frac \* \(float\)a, aq->have\);"#)
    try code(face, #"const char \*unit = wx_sun_value\(sn, wx_count_step\(wx_sun_count_n\(sn\), ta\), txt, sizeof txt\);.*?"#
        + #"set_marker\(sn->frac \* \(float\)a, sn->have && sn->has_now\);"#)
    try code(face, #"int v = wx_count_value\(w->kmh, wx_count_step\(wx_count_n\(w->kmh\), ta\)\);\s*rgb = wx_wind_rgb\(v\);.*?"#
        + #"set_marker\(w->frac \* \(float\)a, w->have\);\s*set_wind\(w, rgb, fo, sprite_opa\);"#)
    try code(face, #"const char \*unit = wx_cast_value\(c, wx_count_step\(wx_cast_count_n\(c\), ta\), txt, sizeof txt, &rgb\);.*?"#
        + #"place_marker\(mx, my, c->have\);\s*set_drop\(c, ta, fo\);"#)
    try code(face, #"int k = wx_count_step\(wx_count_n\(r->pop\), ta\);\s*snprintf\(txt, sizeof txt, "%d", wx_count_value\(r->pop, k\)\);.*?"#
        + #"set_marker\(r->pop / 100\.0f \* \(float\)a, r->has_pop\);"#)
    try code(face, #"int k = wx_count_step\(wx_count_n\(t->value\), ta\);\s*int v = wx_count_value\(t->value, k\);\s*"#
        + #"uint32_t rgb = t->still \? GREY_RGB : wx_temp_rgb\(\(float\)v\);"#)
    try code(face, #"if \(temp\) set_marker\(t->frac \* \(float\)a, t->have\);"#)
    try code(face, #"int q = \(int\)lroundf\(top \* 2\);.*?wx_paint_mercury\(&s_thermo_img, q / 2\.0f, rgb\);"#)
    try code("wx_cast.c", #"int wx_cast_count_n\(const wx_cast_t \*c\) \{ return c->has_count \|\| c->dry \? c->count : 0; \}"#)
    try code("wx_cast.c", #"int v = k < 0 \? 0 : k > c->count \? c->count : k;\s*if \(c->dry && v == c->count\) snprintf\(buf, n, "%d\+", c->count / 2\);\s*"#
        + #"else wx_cast_count_text\(c->hours, v, buf, n\);"#)
    try code("wx_sun.c", #"int wx_sun_count_n\(const wx_sun_t \*s\) \{ return wx_count_n\(s->count\); \}"#)
    try code("wx_sun.c", #"int v = wx_count_value\(s->count, k\);\s*if \(s->hours\) wx_cast_count_text\(true, v, buf, n\);\s*else snprintf\(buf, n, "%d", v\);"#)
}

@Test(cinder) func castDropMatchesCinder() throws {
    #expect(try define("include/wx_cast.h", "WX_CAST_DROP_IN_S") == KnobCastFace.dropInS)
    #expect(try define("include/wx_cast.h", "WX_CAST_DROP_OUT_S") == KnobCastFace.dropOutS)
    #expect(try define("wx_paint.c", "DROP_HEAD") == KnobCastFace.dropHead && define("wx_paint.c", "DROP_TAIL") == -KnobCastFace.dropTail[0].x)
    try code("wx_cast.c", #"double x = 3 \* v \* v \* u \* 0\.45 \+ 3 \* v \* u \* u \* 0\.25 \+ u \* u \* u;.*?return 3 \* v \* u \* u \+ u \* u \* u;"#)
    try code("wx_cast.c", #"return c->now_deg \+ \(c->to_deg - c->now_deg\) \* \(float\)wx_cast_ease\(t / WX_CAST_SLIDE_S\);"#)
    try code("wx_cast.c", #"if \(!\(t >= 0\) \|\| t >= WX_CAST_DROP_OUT_S\) return 0;\s*"#
        + #"if \(t < WX_CAST_DROP_IN_S\) return ramp\(t, 0, WX_CAST_DROP_IN_S, true\);\s*"#
        + #"return t <= WX_CAST_SLIDE_S \? 255 : ramp\(t, WX_CAST_SLIDE_S, WX_CAST_DROP_OUT_S, false\);"#)
    try code(face, #"int opa = c->has_count \? wx_cast_drop_opa\(ta\) \* fo / 255 : 0;\s*show_obj\(s_drop, opa > 0\);\s*if \(opa <= 0\) return;\s*"#
        + #"int q = \(int\)lroundf\(wx_cast_drop_deg\(c, ta\) \* 4\);"#)
    try code(face, #"s_marker = canvas\(s_root, LV_COLOR_FORMAT_RGB565A8\);\s*s_drop = canvas\(s_root, LV_COLOR_FORMAT_RGB565A8\);"#)
    try code("wx_paint.c", #"wx_cast_ring_xy\(deg, &hx, &hy\);.*?const float a = deg \* DEG, ca = cosf\(a\), sa = sinf\(a\);\s*"#
        + #"const uint16_t blue = wx_rgb565\(WX_CAST_RGB\);.*?float lx = dx \* ca \+ dy \* sa, ly = -dx \* sa \+ dy \* ca;\s*"#
        + #"float d = hypotf\(lx, ly\) - DROP_HEAD;\s*if \(lx < -DROP_TAIL\) d = fminf\(d, hypotf\(lx \+ DROP_TAIL, ly\)\);\s*"#
        + #"else if \(lx <= 0\) d = fminf\(d, fabsf\(ly\) - drop_hw_at\(lx\)\);\s*int i = y \* im->w \+ x;\s*"#
        + #"put\(im, i, 0, cov\(d - 1\)\);\s*put\(im, i, blue, cov\(d \+ 1\)\);"#)
    try code("wx_cast.c", #"\*x = 233\.0f \+ 222\.0f \* sinf\(a\);\s*\*y = 233\.0f - 222\.0f \* cosf\(a\);"#)
    let hw = try table("wx_paint.c", "const float wx_drop_hw[WX_DROP_HW_N]").items
    #expect(hw.count == Int(try define("include/wx_paint.h", "WX_DROP_HW_N")))
    let p = KnobCastFace.dropTail
    for (i, v) in hw.enumerated() {
        guard case .num(let want) = v else { Issue.record("wx_drop_hw[\(i)]: \(v)"); continue }
        let x = p[0].x + (p[3].x - p[0].x) * Double(i) / Double(hw.count - 1)
        var lo = 0.0, hi = 1.0
        for _ in 0..<60 {
            let u = (lo + hi) / 2, w = 1 - u
            if w * w * w * p[0].x + 3 * w * w * u * p[1].x + 3 * w * u * u * p[2].x + u * u * u * p[3].x < x { lo = u } else { hi = u }
        }
        let u = (lo + hi) / 2, w = 1 - u
        let y = w * w * w * p[0].y + 3 * w * w * u * p[1].y + 3 * w * u * u * p[2].y + u * u * u * p[3].y
        #expect(abs(y - want) < 1e-3, "the drop tail at x \(x): \(y), cinder \(want)")
    }
}
