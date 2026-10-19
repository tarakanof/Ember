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
    #expect(try define("include/wx_paint.h", "WX_RAMP") == KnobRainFace.ramp)
    try code("wx_rain.c", #"static int offset\(int frames, int a\) \{ return \(int\)lround\(\(double\)WX_SHEET_PERIOD \* a / frames\) % WX_SHEET_PERIOD; \}"#)
    try code("wx_rain.c", #"int f = 2 \* SHEETS\[s\]\.tile_frames;\s*return r && r->kind == WX_PRECIP_SNOW \? 2 \* f : f;"#)
    try code("wx_rain.c", #"f->off\[s\] = offset\(frames, \(k \+ SHEETS\[s\]\.delay_frames\) % frames\);"#)
    try code("wx_rain.c", #"ops->strip_top\[s\]\[0\] = f->off\[s\] - WX_SHEET_PERIOD;\s*ops->strip_top\[s\]\[1\] = f->off\[s\];"#)
}

@Test(cinder) func noDripSplashOrBoltShowsAtFrameZero() throws {
    try code("wx_rain.c", #"float u = \(x - 233\.0f\) / 128\.0f;\s*float k = 1 - u \* u;\s*return 198\.0f - 103\.0f \* sqrtf\(k > 0 \? k : 0\);"#)
    try code("wx_rain.c", #"const wx_drop_t \*d = &TOP\[SPLASH_DROP\[n\]\];"#)
    try code("wx_rain.c", #"float ty = d->y \+ \(d->tile \? WX_SHEET_H : 0\);\s*"#
        + #"float t = fmodf\(wx_canopy_y\(d->x\) - 2 - \(float\)s->box\.y0 - ty - d->len, \(float\)WX_SHEET_PERIOD\);\s*"#
        + #"if \(t < 0\) t \+= WX_SHEET_PERIOD;\s*int frames = 2 \* s->tile_frames;\s*"#
        + #"for \(int a = 0; a < frames; a\+\+\)\s*if \(\(float\)offset\(frames, a\) >= t\) return a;"#)
    try code("wx_rain.c", #"int a = \(k \+ SHEETS\[at\.sheet\]\.delay_frames\) % frames;\s*"#
        + #"int j = \(\(a - wx_splash_hit\(n\)\) % frames \+ frames\) % frames;\s*if \(j >= 3\) continue;"#)
    try code("wx_rain.c", #"int j = k % DRIP\[i\]\.period - DRIP\[i\]\.start;\s*if \(snow \|\| j < 0 \|\| j >= 7\) continue;"#)
    try code("wx_rain.c", #"for \(int i = 0; i < 6; i\+\+\) f->bolt \|= k % WX_STORM_FRAMES == FLASH\[i\];"#)
    let top = KnobRainFace.sheets[2], period = Float(2 * KnobRainFace.sheetH), frames = 2 * top.tileFrames
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
