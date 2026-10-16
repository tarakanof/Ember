import CoreText
import ImageIO
import SwiftUI

public enum KnobFace: Sendable, Equatable {
    case bot(pose: BotPose, mood: KnobMood, glint: Double? = nil)
    case pomodoro(KnobPomoFace)
    case nowPlaying(KnobNowPlayingFace)
    case weather(KnobTempFace)
}

public struct KnobFaceView: View, Equatable {
    let face: KnobFace
    let theme: KnobTheme
    let brightness: Double

    public init(_ face: KnobFace, theme: KnobTheme = .standard, brightness: Double = 1) {
        self.face = face; self.theme = theme; self.brightness = brightness
        _ = KnobFaceRender.fontRegistered
    }

    public var body: some View {
        Canvas { ctx, size in
            let d = theme.screen.diameterPx
            let side = min(size.width, size.height)
            ctx.translateBy(x: (size.width - side) / 2, y: (size.height - side) / 2)
            ctx.scaleBy(x: side / d, y: side / d)
            let disc = Path(ellipseIn: CGRect(x: 0, y: 0, width: d, height: d))
            ctx.fill(disc, with: .color(.black))
            ctx.clip(to: disc)
            switch face {
            case .bot(let pose, let mood, let glint): KnobFaceRender.bot(pose, mood, glint: glint, theme, in: &ctx)
            case .pomodoro(let p): KnobFaceRender.pomodoro(p, theme, in: &ctx)
            case .nowPlaying(let n): KnobFaceRender.nowPlaying(n, theme, in: &ctx)
            case .weather(let t): KnobFaceRender.weather(t, theme, in: &ctx)
            }
            let dim = (1 - min(max(brightness, 0), 1)) * 0.6
            if dim > 0 { ctx.fill(disc, with: .color(.black.opacity(dim))) }
        }
        .aspectRatio(1, contentMode: .fit)
    }
}

extension Color {
    init(_ c: RGB, opacity: Double = 1) {
        self.init(.sRGB, red: Double(c.r) / 255, green: Double(c.g) / 255, blue: Double(c.b) / 255, opacity: opacity)
    }
}

enum KnobFaceRender {
    static let shapeCache = KnobBotShape(theme: KnobTheme.standard.bot)

    static let fontRegistered: Bool = {
        guard let url = KnobTheme.fontURL else { return false }
        return CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
    }()

    static let rimPaths: [[Path]] = rimPaths(KnobTheme.standard, shape: shapeCache)

    static func rimPaths(_ theme: KnobTheme, shape: KnobBotShape) -> [[Path]] {
        let b = theme.bot
        let c = theme.screen.diameterPx / 2, r = c * b.fill
        let variants = KnobBotShape.rimVariants(steps: b.rimSteps, squash: b.hop.squash)
        return [0.0, 1.0].map { k in
            let ring = shape.ring(k)
            return variants.map { v in
                var p = polyline(ring.map { q in
                    CGPoint(x: c + r * v.sx * q.x, y: c - r * (-(1 - v.sy) + v.sy * q.y))
                })
                p.closeSubpath()
                return p
            }
        }
    }

    static func label(_ s: String, _ px: Double, _ c: RGB, centerX: Double, anchor: Double = 0.5,
                      top: Double, clip: Double? = nil, theme: KnobTheme, in ctx: inout GraphicsContext) {
        guard !s.isEmpty else { return }
        let m = theme.font.metrics(px)
        let font: Font = fontRegistered ? .custom(theme.font.family, fixedSize: px) : .system(size: px, weight: .medium)
        let text = ctx.resolve(Text(verbatim: s).font(font).foregroundStyle(Color(c)))
        let size = text.measure(in: CGSize(width: 2000, height: 2000))
        let width = size.width
        let baseline = top + m.lineHeightPx - m.baselinePx
        let rect = CGRect(x: centerX - width * anchor, y: baseline - text.firstBaseline(in: size),
                          width: size.width, height: size.height)
        var c2 = ctx
        if let clip { c2.clip(to: Path(CGRect(x: centerX - clip / 2, y: top, width: clip, height: m.lineHeightPx))) }
        c2.draw(text, in: rect)
    }

    static func centredTop(_ px: Double, dy: Double, theme: KnobTheme) -> Double {
        let lh = theme.font.metrics(px).lineHeightPx
        return ((theme.screen.diameterPx - lh) / 2).rounded(.down) + dy
    }

    static func polyline(_ pts: [CGPoint]) -> Path {
        var p = Path()
        guard let first = pts.first else { return p }
        p.move(to: first)
        if pts.count == 1 { p.addLine(to: first) }
        for q in pts.dropFirst() { p.addLine(to: q) }
        return p
    }

    static func bot(_ p: BotPose, _ mood: KnobMood, glint: Double? = nil, _ theme: KnobTheme,
                    in ctx: inout GraphicsContext) {
        let b = theme.bot
        let c = theme.screen.diameterPx / 2
        let r = c * b.fill
        let standard = b == KnobTheme.standard.bot && theme.screen == KnobTheme.standard.screen
        let shape = standard ? shapeCache : KnobBotShape(theme: b)
        let paths = standard ? rimPaths : rimPaths(theme, shape: shape)
        let variants = KnobBotShape.rimVariants(steps: b.rimSteps, squash: b.hop.squash)
        let rimDY = Double(Int((-r * p.offsetY * b.hopScale).rounded()) & ~1)
        let color = theme.moodColor(p.mood)
        let rim = color.scaled(p.mood == .idle || p.mood == .sleepy ? b.rimDimGain : 1)

        var outline = paths[p.triangle >= 0.5 ? 1 : 0][KnobBotShape.rimVariant(for: p, variants: variants)]
        outline = outline.offsetBy(dx: 0, dy: rimDY)
        ctx.fill(outline, with: .color(Color(b.bodyColor)))
        ctx.stroke(outline, with: .color(Color(rim)), style: StrokeStyle(lineWidth: b.rimPx, lineJoin: .round))
        if let glint, p.mood == .working, p.triangle < 0.5, rimDY == 0,
           KnobBotShape.rimVariant(for: p, variants: variants) == b.rimSteps {
            Self.glint(head: glint, center: CGPoint(x: c, y: c), radius: r, color: color, b.glint, in: &ctx)
        }

        func screen(_ q: CGPoint) -> CGPoint {
            let by = p.offsetY * b.hopScale - (1 - p.scaleY) + p.scaleY * q.y
            return CGPoint(x: c + r * (p.offsetX + p.scaleX * q.x), y: c - r * by)
        }
        let k = r * (p.scaleX + p.scaleY) / 2
        let d = theme.screen.diameterPx
        for eye in KnobBotShape.eyes(p, scale: b.eyeScale, geometry: b.eyes) {
            let pts = eye.points.map(screen), width = eye.width * k
            var lidded = ctx
            if let y = KnobBotShape.lidLineY(pts.map { Double($0.y) }, width: width, lid: eye.lid, keep: eye.keep) {
                lidded.clip(to: Path(CGRect(x: 0, y: y, width: d, height: max(d - y, 0))))
            }
            lidded.stroke(polyline(pts), with: .color(Color(b.eyeColor)),
                          style: StrokeStyle(lineWidth: width, lineCap: .round, lineJoin: .round))
        }

        if p.badge > 0.01 {
            let at = CGPoint(x: c + r * cos(.pi / 4) * b.badge.at, y: c - r * sin(.pi / 4) * b.badge.at + rimDY)
            func disc(_ rad: Double) -> Path {
                let rr = rad.rounded()
                return Path(ellipseIn: CGRect(x: at.x.rounded() - rr, y: at.y.rounded() - rr, width: 2 * rr, height: 2 * rr))
            }
            ctx.fill(disc(b.badge.gap * p.badge * r), with: .color(.black))
            ctx.fill(disc(b.badge.dot * p.badge * r), with: .color(Color(color)))
        }
        if showsLabel(mood, rim: p.mood) {
            arcLabel(mood, moodColor: color, center: CGPoint(x: c, y: c + rimDY), theme: theme, in: &ctx)
        }
    }

    static func glint(head: Double, center: CGPoint, radius: Double, color: RGB, _ g: KnobTheme.Bot.Glint,
                      in ctx: inout GraphicsContext) {
        func mix(_ v: UInt8) -> UInt8 { UInt8((Double(v) + (255 - Double(v)) * g.whiteMix).rounded()) }
        let c = Color(RGB(r: mix(color.r), g: mix(color.g), b: mix(color.b)))
        func pt(_ deg: Double) -> CGPoint {
            let a = deg * .pi / 180
            return CGPoint(x: center.x + radius * cos(a), y: center.y + radius * sin(a))
        }
        var arc = Path()
        arc.move(to: pt(head - g.tailDeg))
        for k in stride(from: g.tailDeg - 1, through: 0, by: -1) { arc.addLine(to: pt(head - k)) }
        let tail = 1 - g.tailDeg / 360
        let shading = GraphicsContext.Shading.conicGradient(
            Gradient(stops: [.init(color: c.opacity(0), location: 0), .init(color: c.opacity(0), location: tail),
                             .init(color: c, location: 1)]),
            center: center, angle: .degrees(head))
        ctx.stroke(arc, with: shading, style: StrokeStyle(lineWidth: g.widthPx, lineCap: .butt, lineJoin: .round))
        let h = pt(head), hw = g.widthPx / 2
        ctx.fill(Path(ellipseIn: CGRect(x: h.x - hw, y: h.y - hw, width: 2 * hw, height: 2 * hw)), with: .color(c))
    }

    static let t3Icon = (["........", "XXX.XXX.", ".X....X.", ".X...XX.", ".X....X.", ".X..XXX.", "........", "........"],
                         ["........", "....XXX.", "......X.", ".....XX.", "......X.", "....XXX.", "........", "........"])

    static let toolMarks: [String: CGImage] = {
        var out: [String: CGImage] = [:]
        for tool in ["claude", "codex"] {
            if let url = Bundle.module.url(forResource: "knob-mark-" + tool, withExtension: "png"),
               let src = CGImageSourceCreateWithURL(url as CFURL, nil), let img = CGImageSourceCreateImageAtIndex(src, 0, nil) {
                out[tool] = img
            }
        }
        return out
    }()

    static func showsLabel(_ mood: KnobMood, rim: BotMood) -> Bool {
        mood.showsHost && KnobMood.labelMoods.contains(rim)
    }

    static func labelCanvas(center: CGPoint, theme: KnobTheme) -> CGRect {
        let h = theme.bot.host
        return CGRect(x: center.x - h.canvasWPx / 2, y: center.y - theme.screen.diameterPx / 2 + h.canvasYPx,
                      width: h.canvasWPx, height: h.canvasHPx)
    }

    static func arcLabel(_ mood: KnobMood, moodColor: RGB, center: CGPoint, theme: KnobTheme,
                         in ctx: inout GraphicsContext) {
        let h = theme.bot.host
        let textColor = mood.hostColor ?? moodColor
        let sub = mood.shownSub
        var ctx = ctx
        ctx.clip(to: Path(labelCanvas(center: center, theme: theme)))
        let markBottom = h.markBottom(hostLine: !sub.isEmpty)
        let box = CGRect(x: center.x - h.markHPx / 2, y: center.y + markBottom - h.markHPx, width: h.markHPx, height: h.markHPx)
        let marked = KnobMood.marksTool(mood.mood)
        if marked, let mark = toolMarks[mood.tool] {
            let r = CGRect(x: center.x - Double(mark.width) / 2, y: box.maxY - Double(mark.height),
                           width: Double(mark.width), height: Double(mark.height))
            var c2 = ctx
            let brand = mood.tool == "claude" ? h.claudeColor : h.codexColor
            c2.clipToLayer(opacity: 1) { l in l.draw(Image(decorative: mark, scale: 1), in: r) }
            c2.fill(Path(r), with: .color(Color(brand)))
        } else if marked && mood.tool == "t3" {
            let (body, feature) = t3Icon
            let off = (h.markHPx - 8 * h.iconCellPx) / 2
            for (rows, color) in [(body, textColor), (feature, moodColor)] {
                var cells = Path()
                for (y, row) in rows.enumerated() {
                    for (x, ch) in row.enumerated() where ch == "X" {
                        cells.addRect(CGRect(x: box.minX + off + Double(x) * h.iconCellPx,
                                             y: box.minY + off + Double(y) * h.iconCellPx, width: h.iconCellPx, height: h.iconCellPx))
                    }
                }
                ctx.fill(cells, with: .color(Color(color)))
            }
        }
        arcLine(mood.host, fontPx: h.fontPx, radius: h.radiusPx, center: center, color: textColor, theme: theme, in: &ctx)
        arcLine(sub, fontPx: h.subFontPx, radius: h.subRadiusPx, center: center, color: textColor, theme: theme, in: &ctx)
    }

    static func arcLine(_ text: String, fontPx: Double, radius: Double, center: CGPoint, color: RGB, theme: KnobTheme,
                        in ctx: inout GraphicsContext) {
        guard !text.isEmpty else { return }
        let font: Font = fontRegistered ? .custom(theme.font.family, fixedSize: fontPx) : .system(size: fontPx, weight: .medium)
        let glyphs = text.map { ctx.resolve(Text(verbatim: String($0)).font(font).foregroundStyle(Color(color))) }
        let sizes = glyphs.map { $0.measure(in: CGSize(width: 200, height: 200)) }
        let total = sizes.map(\.width).reduce(0, +), k = 180 / .pi / radius
        var s = 0.0
        for (i, g) in glyphs.enumerated() {
            let size = sizes[i]
            let th = 90 + total / 2 * k - (s + size.width / 2) * k, a = th * .pi / 180
            s += size.width
            var c2 = ctx
            c2.translateBy(x: center.x + radius * cos(a), y: center.y + radius * sin(a))
            c2.rotate(by: .degrees(th - 90))
            c2.draw(g, in: CGRect(x: -size.width / 2, y: -g.firstBaseline(in: size), width: size.width, height: size.height))
        }
    }

    static func pomodoro(_ f: KnobPomoFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let t = theme.pomodoro
        let c = theme.screen.diameterPx / 2
        let center = CGPoint(x: c, y: c)
        var track = Path()
        track.addArc(center: center, radius: t.ringRadiusPx, startAngle: .zero, endAngle: .degrees(360), clockwise: false)
        ctx.stroke(track, with: .color(Color(f.track)), lineWidth: t.ringWidthPx)
        if let arc = f.arc, f.fraction > 0 {
            var p = Path()
            p.addArc(center: center, radius: t.ringRadiusPx, startAngle: .degrees(-90),
                     endAngle: .degrees(-90 + 360 * f.fraction), clockwise: false)
            ctx.stroke(p, with: .color(Color(arc)), style: StrokeStyle(lineWidth: t.ringWidthPx, lineCap: .round))
        }
        for (text, l, color) in [(f.round, t.round, t.colors.textDim), (f.time, t.time, f.timeColor),
                                 (f.phase, t.phase, f.phaseColor)] {
            label(text, l.fontPx, color, centerX: c, top: centredTop(l.fontPx, dy: l.dyPx, theme: theme),
                  clip: l.widthPx, theme: theme, in: &ctx)
        }
    }

    static func fit(_ text: String, _ px: Double, width: Double, theme: KnobTheme) -> String {
        let font = CTFontCreateWithName(theme.font.family as CFString, px, nil)
        func w(_ s: String) -> Double {
            let line = CTLineCreateWithAttributedString(NSAttributedString(string: s, attributes: [.font: font]))
            return CTLineGetTypographicBounds(line, nil, nil, nil)
        }
        guard w(text) > width else { return text }
        let chars = Array(text)
        var lo = 0, hi = chars.count
        while lo < hi {
            let mid = (lo + hi + 1) / 2
            if w(String(chars[..<mid]) + "...") <= width { lo = mid } else { hi = mid - 1 }
        }
        while lo > 0, chars[lo - 1] == " " || chars[lo - 1] == "-" { lo -= 1 }
        return String(chars[..<lo]) + "..."
    }

    static func nowPlaying(_ f: KnobNowPlayingFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let t = theme.nowplaying
        let d = theme.screen.diameterPx
        let c = d / 2
        guard f.mode != .idle else {
            label(f.idleLine, t.idle.fontPx, t.colors.idle, centerX: c,
                  top: centredTop(t.idle.fontPx, dy: t.idle.dyPx, theme: theme), clip: t.idle.widthPx, theme: theme, in: &ctx)
            return
        }
        let playing = f.mode == .playing
        let arc = playing ? t.colors.arc : t.colors.arcPaused
        let center = CGPoint(x: c, y: c)

        if let b = f.pictures.backdrop {
            ctx.drawLayer { l in
                l.clip(to: Path(ellipseIn: CGRect(x: c - t.backdropDiskRadiusPx, y: c - t.backdropDiskRadiusPx,
                                                   width: 2 * t.backdropDiskRadiusPx, height: 2 * t.backdropDiskRadiusPx)))
                l.draw(Image(decorative: b.image, scale: 1), in: CGRect(x: 0, y: 0, width: d, height: d))
            }
        }
        var track = Path()
        track.addArc(center: center, radius: t.ringRadiusPx, startAngle: .zero, endAngle: .degrees(360), clockwise: false)
        ctx.stroke(track, with: .color(Color(t.colors.track)), lineWidth: t.ringWidthPx)
        if f.fraction > 0 {
            var p = Path()
            p.addArc(center: center, radius: t.ringRadiusPx, startAngle: .degrees(-90),
                     endAngle: .degrees(-90 + 360 * f.fraction), clockwise: false)
            ctx.stroke(p, with: .color(Color(arc)), style: StrokeStyle(lineWidth: t.ringWidthPx, lineCap: .round))
        }

        let album = CGRect(x: c - t.albumPx / 2, y: c + t.albumDyPx - t.albumPx / 2, width: t.albumPx, height: t.albumPx)
        ctx.drawLayer { l in
            l.clip(to: Path(ellipseIn: album))
            if let a = f.pictures.album {
                l.draw(Image(decorative: a.image, scale: 1), in: album)
            } else {
                l.fill(Path(album), with: .color(Color(t.colors.placeholder)))
                let note = l.resolve(Text(Image(systemName: "music.note")).font(.system(size: 48))
                    .foregroundStyle(Color(t.colors.note)))
                l.draw(note, at: CGPoint(x: album.midX, y: album.midY))
            }
        }

        let a = (-90 + 360 * f.avatarFraction) * .pi / 180
        let at = CGPoint(x: c + t.ringRadiusPx * cos(a), y: c + t.ringRadiusPx * sin(a))
        if let p = f.pictures.artist {
            let r = CGRect(x: at.x - t.artistPx / 2, y: at.y - t.artistPx / 2, width: t.artistPx, height: t.artistPx)
            ctx.drawLayer { l in
                l.clip(to: Path(ellipseIn: r))
                l.draw(Image(decorative: p.image, scale: 1), in: r)
            }
        } else {
            ctx.fill(Path(ellipseIn: CGRect(x: at.x - t.dotPx / 2, y: at.y - t.dotPx / 2, width: t.dotPx, height: t.dotPx)),
                     with: .color(Color(arc)))
        }

        for (text, l, color) in [(f.title, t.title, t.colors.text), (f.sub, t.sub, t.colors.sub), (f.meta, t.meta, t.colors.meta)] {
            label(fit(text, l.fontPx, width: l.widthPx, theme: theme), l.fontPx, color, centerX: c,
                  top: centredTop(l.fontPx, dy: l.dyPx, theme: theme), clip: l.widthPx, theme: theme, in: &ctx)
        }
    }

    static func weather(_ f: KnobTempFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let w = theme.weather, th = w.thermo
        let c = theme.screen.diameterPx / 2
        func disc(_ p: CGPoint, _ r: Double) -> Path { Path(ellipseIn: CGRect(x: p.x - r, y: p.y - r, width: 2 * r, height: 2 * r)) }

        for k in 0..<w.dots.faces {
            ctx.fill(disc(KnobTempFace.dotPoint(k, w.dots, center: c), w.dots.dotRadiusPx),
                     with: .color(Color(k == 0 ? w.dots.onColor : w.dots.offColor)))
        }

        let head = CGPoint(x: c, y: th.headYPx + th.dyPx), bulb = CGPoint(x: c, y: th.bulbYPx + th.dyPx)
        let tube = CGRect(x: c - th.tubeHalfWPx, y: th.tubeMidYPx - th.tubeHalfHPx + th.dyPx,
                          width: 2 * th.tubeHalfWPx, height: 2 * th.tubeHalfHPx)
        let half = th.strokePx / 2
        for p in [disc(head, th.headRadiusPx), Path(tube), disc(bulb, th.bulbRadiusPx)] {
            ctx.stroke(p, with: .color(Color(th.strokeColor)), lineWidth: th.strokePx)
        }
        for p in [disc(head, th.headRadiusPx - half), Path(tube.insetBy(dx: half, dy: half)), disc(bulb, th.bulbRadiusPx - half)] {
            ctx.fill(p, with: .color(.black))
        }

        var arc = Path()
        arc.addArc(center: CGPoint(x: c, y: c), radius: w.ring.radiusPx, startAngle: .degrees(w.ring.fromDeg - 90),
                   endAngle: .degrees(w.ring.fromDeg - 90 + w.ring.sweepDeg), clockwise: false)
        let ringStyle = StrokeStyle(lineWidth: w.ring.widthPx, lineCap: .round)
        if f.value == nil {
            ctx.stroke(arc, with: .color(Color(w.ring.trackColor)), style: ringStyle)
        } else {
            ctx.stroke(arc, with: ringShading(f, w, center: c), style: ringStyle)
        }

        let merc = f.valueColor(w)
        let top = f.value.map { KnobTempFace.mercuryTop(Double($0), th) } ?? th.mercuryBottomPx
        let hh = (th.mercuryBottomPx - top) / 2, mid = (top + th.mercuryBottomPx) / 2 + th.dyPx
        let reach = max(hh - th.mercuryHalfWPx, 0) + th.mercuryHalfWPx
        ctx.fill(Path(roundedRect: CGRect(x: c - th.mercuryHalfWPx, y: mid - reach, width: 2 * th.mercuryHalfWPx, height: 2 * reach),
                      cornerRadius: th.mercuryHalfWPx), with: .color(Color(merc)))
        ctx.fill(disc(bulb, th.mercuryBulbRadiusPx), with: .color(Color(merc)))
        for tick in th.ticksPx {
            var p = Path()
            p.move(to: CGPoint(x: th.tickXPx[0], y: tick + th.dyPx))
            p.addLine(to: CGPoint(x: th.tickXPx[1], y: tick + th.dyPx))
            ctx.stroke(p, with: .color(Color(top <= tick ? merc : th.tickColor)),
                       style: StrokeStyle(lineWidth: th.tickWidthPx, lineCap: .round))
        }

        let v = w.value
        spacedLabel(f.valueText(), v.fontPx, merc, centerX: c + degreeShift(v, theme), spacing: v.letterSpacePx, top: v.yPx,
                    theme: theme, in: &ctx)
        if f.value != nil && f.hasRange {
            let e = w.ends, d = theme.screen.diameterPx
            label(KnobTempFace.degrees(f.lo), e.fontPx, KnobTempFace.color(Double(f.lo), w.scale), centerX: e.xPx, anchor: 0,
                  top: e.yPx, theme: theme, in: &ctx)
            label(KnobTempFace.degrees(f.hi), e.fontPx, KnobTempFace.color(Double(f.hi), w.scale), centerX: d - e.xPx, anchor: 1,
                  top: e.yPx, theme: theme, in: &ctx)
        }
        label(f.sub, w.sub.fontPx, w.sub.color, centerX: c, top: w.sub.yPx, theme: theme, in: &ctx)

        if f.value != nil {
            let p = KnobTempFace.gaugePoint(f.fraction, w, center: c)
            let at = CGPoint(x: p.x.rounded(), y: p.y.rounded())
            ctx.fill(disc(at, w.marker.discRadiusPx), with: .color(.black))
            ctx.stroke(disc(at, w.marker.ringRadiusPx), with: .color(Color(w.marker.color)), lineWidth: w.marker.ringWidthPx)
        }
    }

    static func ringShading(_ f: KnobTempFace, _ w: KnobTheme.Weather, center c: Double) -> GraphicsContext.Shading {
        let sweep = w.ring.sweepDeg, lo = Double(f.lo), hi = Double(f.hi)
        var stops = stride(from: 0.0, through: sweep, by: 2).map { s in
            Gradient.Stop(color: Color(KnobTempFace.color(lo + (hi - lo) * s / sweep, w.scale)), location: s / 360)
        }
        let gap = (sweep + 360) / 2 / 360
        stops += [.init(color: Color(KnobTempFace.color(hi, w.scale)), location: gap),
                  .init(color: Color(KnobTempFace.color(lo, w.scale)), location: gap),
                  .init(color: Color(KnobTempFace.color(lo, w.scale)), location: 1)]
        return .conicGradient(Gradient(stops: stops), center: CGPoint(x: c, y: c), angle: .degrees(w.ring.fromDeg - 90))
    }

    static func spacedLabel(_ s: String, _ px: Double, _ c: RGB, centerX: Double, spacing: Double, top: Double,
                            theme: KnobTheme, in ctx: inout GraphicsContext) {
        let m = theme.font.metrics(px)
        let ct = CTFontCreateWithName(theme.font.family as CFString, px, nil)
        let font: Font = fontRegistered ? .custom(theme.font.family, fixedSize: px) : .system(size: px, weight: .medium)
        let chars = s.map(String.init)
        let advances = chars.map { ch in
            CTLineGetTypographicBounds(CTLineCreateWithAttributedString(NSAttributedString(string: ch, attributes: [.font: ct])),
                                       nil, nil, nil).rounded()
        }
        let width = advances.reduce(0, +) + spacing * Double(max(chars.count - 1, 0))
        var x = centerX - (width / 2).rounded(.down)
        let baseline = top + m.lineHeightPx - m.baselinePx
        for (ch, adv) in zip(chars, advances) {
            let text = ctx.resolve(Text(verbatim: ch).font(font).foregroundStyle(Color(c)))
            let size = text.measure(in: CGSize(width: 2000, height: 2000))
            ctx.draw(text, in: CGRect(x: x, y: baseline - text.firstBaseline(in: size), width: size.width, height: size.height))
            x += adv + spacing
        }
    }

    static func degreeShift(_ v: KnobTheme.Weather.Value, _ theme: KnobTheme) -> Double {
        _ = fontRegistered
        let font = CTFontCreateWithName(theme.font.family as CFString, v.fontPx, nil)
        let line = CTLineCreateWithAttributedString(NSAttributedString(string: "°", attributes: [.font: font]))
        let deg = Int(CTLineGetTypographicBounds(line, nil, nil, nil).rounded()) + Int(v.letterSpacePx)
        return Double(deg / 2)
    }
}
