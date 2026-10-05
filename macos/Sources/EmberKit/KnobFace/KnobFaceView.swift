import CoreText
import ImageIO
import SwiftUI

/// One frame of a knob page, as data.
public enum KnobFace: Sendable, Equatable {
    /// `glint`: the working glint's head angle in screen degrees (0 = 3 o'clock,
    /// clockwise), nil for none.
    case bot(pose: BotPose, mood: KnobMood, glint: Double? = nil)
    case pomodoro(KnobPomoFace)
    case nowPlaying(KnobNowPlayingFace)
    /// `tempC` nil prints "--°".
    case weather(look: KnobWeatherLook, draws: [KnobWeatherScene.Draw], tempC: Double?)
}

/// Draws a `KnobFace` on the knob's 466 px round screen, scaled to fit.
/// `brightness` (0…1) dims it like the panel's backlight, never to black.
/// Equal inputs skip the redraw.
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
            case .weather(let look, let draws, let temp): KnobFaceRender.weather(look, draws, temp, theme, in: &ctx)
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

    /// Registers the bundled Montserrat once per process.
    static let fontRegistered: Bool = {
        guard let url = KnobTheme.fontURL else { return false }
        return CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
    }()

    /// The firmware's outline images for the standard theme, [shape][variant],
    /// in screen pixels before the hop offset.
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

    /// A one-line LVGL label: `top` is its box's top edge; the baseline sits
    /// the font's line height minus its baseline offset below that.
    static func label(_ s: String, _ px: Double, _ c: RGB, centerX: Double, top: Double, clip: Double? = nil,
                      theme: KnobTheme, in ctx: inout GraphicsContext) {
        guard !s.isEmpty else { return }
        let m = theme.font.metrics(px)
        let font: Font = fontRegistered ? .custom(theme.font.family, fixedSize: px) : .system(size: px, weight: .medium)
        let text = ctx.resolve(Text(verbatim: s).font(font).foregroundStyle(Color(c)))
        let size = text.measure(in: CGSize(width: 2000, height: 2000))
        let baseline = top + m.lineHeightPx - m.baselinePx
        let rect = CGRect(x: centerX - size.width / 2, y: baseline - text.firstBaseline(in: size),
                          width: size.width, height: size.height)
        var c2 = ctx
        if let clip { c2.clip(to: Path(CGRect(x: centerX - clip / 2, y: top, width: clip, height: m.lineHeightPx))) }
        c2.draw(text, in: rect)
    }

    /// The top of a label LVGL centres on the screen with offset `dy`.
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

    // MARK: Bot

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
        // The firmware draws the glint on the base circle only (no hop, no squash).
        if let glint, p.mood == .working, p.triangle < 0.5, rimDY == 0,
           KnobBotShape.rimVariant(for: p, variants: variants) == b.rimSteps {
            Self.glint(head: glint, center: CGPoint(x: c, y: c), radius: r, color: color, b.glint, in: &ctx)
        }

        func screen(_ q: CGPoint) -> CGPoint {
            let by = p.offsetY * b.hopScale - (1 - p.scaleY) + p.scaleY * q.y
            return CGPoint(x: c + r * (p.offsetX + p.scaleX * q.x), y: c - r * by)
        }
        let k = r * (p.scaleX + p.scaleY) / 2
        for eye in KnobBotShape.eyes(p, scale: b.eyeScale, geometry: b.eyes) {
            ctx.stroke(polyline(eye.points.map(screen)), with: .color(Color(b.eyeColor)),
                       style: StrokeStyle(lineWidth: eye.width * k, lineCap: .round, lineJoin: .round))
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
        if mood.showsHost && (p.mood == .working || p.mood == .waiting || p.mood == .error) {
            arcLabel(mood, moodColor: color, center: CGPoint(x: c, y: c + rimDY), theme: theme, in: &ctx)
        }
    }

    /// The working glint (cinder `ring_glint.c`): an arc on the ring's centreline
    /// from the head back `tailDeg`, alpha falling linearly along the tail (one
    /// stroke with a conic gradient, so no seams), round head.
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

    /// T3's 8×8 tool icon (internal/render): body rows, then feature rows. Claude and
    /// Codex use the official marks instead (`knob-mark-*.png`, cinder `tool_marks.c`).
    static let t3Icon = (["........", "XXX.XXX.", ".X....X.", ".X...XX.", ".X....X.", ".X..XXX.", "........", "........"],
                         ["........", "....XXX.", "......X.", ".....XX.", "......X.", "....XXX.", "........", "........"])

    /// The official mark's alpha mask (white on alpha, `markPx` square), by tool.
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

    /// The host label (cinder `arc_text.c` + `bot_view.c` label_draw): the text along the
    /// bottom of a circle, baseline on it, tops toward the centre, centred on 6 o'clock,
    /// with the tool's icon upright and centred above it.
    static func arcLabel(_ mood: KnobMood, moodColor: RGB, center: CGPoint, theme: KnobTheme,
                         in ctx: inout GraphicsContext) {
        let h = theme.bot.host
        let textColor = mood.hostColor ?? moodColor
        let font: Font = fontRegistered ? .custom(theme.font.family, fixedSize: h.fontPx)
            : .system(size: h.fontPx, weight: .medium)
        let glyphs = mood.host.map { ctx.resolve(Text(verbatim: String($0)).font(font).foregroundStyle(Color(textColor))) }
        let sizes = glyphs.map { $0.measure(in: CGSize(width: 200, height: 200)) }
        let widths = sizes.map(\.width)
        let total = widths.reduce(0, +), k = 180 / .pi / h.radiusPx
        var s = 0.0
        var items: [(CGPoint, Double)] = []
        for w in widths {
            let th = 90 + total / 2 * k - (s + w / 2) * k, a = th * .pi / 180
            items.append((CGPoint(x: center.x + h.radiusPx * cos(a), y: center.y + h.radiusPx * sin(a)), th - 90))
            s += w
        }
        let box = CGRect(x: center.x - h.markPx / 2, y: center.y + h.markBottomPx - h.markPx, width: h.markPx, height: h.markPx)
        if let mark = toolMarks[mood.tool] {
            var c2 = ctx
            let brand = mood.tool == "claude" ? h.claudeColor : h.codexColor
            c2.clipToLayer(opacity: 1) { l in l.draw(Image(decorative: mark, scale: 1), in: box) }
            c2.fill(Path(box), with: .color(Color(brand)))
        } else if mood.tool == "t3" {
            let (body, feature) = t3Icon
            let off = (h.markPx - 8 * h.iconCellPx) / 2
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
        for (i, g) in glyphs.enumerated() {
            let (p, rot) = items[i]
            var c2 = ctx
            c2.translateBy(x: p.x, y: p.y)
            c2.rotate(by: .degrees(rot))
            let size = sizes[i]
            c2.draw(g, in: CGRect(x: -size.width / 2, y: -g.firstBaseline(in: size), width: size.width, height: size.height))
        }
    }

    // MARK: Pomodoro

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

    // MARK: Now playing

    /// `text` cut to `width` px with "..." as the firmware's `fit_text` does.
    static func fit(_ text: String, _ px: Double, width: Double, theme: KnobTheme) -> String {
        let font = CTFontCreateWithName(theme.font.family as CFString, px, nil)
        func w(_ s: String) -> Double {
            let line = CTLineCreateWithAttributedString(NSAttributedString(string: s, attributes: [.font: font]))
            return CTLineGetTypographicBounds(line, nil, nil, nil)
        }
        guard w(text) > width else { return text }
        let chars = Array(text)
        var lo = 0, hi = chars.count
        while lo < hi {   // the longest prefix that fits with "..."
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

        // The backdrop is masked to a disk; the ring runs in the black band outside it.
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

        // The artist rides the ring at the progress point (a dot without a picture).
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

        // Text last: it may run over the artist picture at 4 to 8 o'clock.
        for (text, l, color) in [(f.title, t.title, t.colors.text), (f.sub, t.sub, t.colors.sub), (f.meta, t.meta, t.colors.meta)] {
            label(fit(text, l.fontPx, width: l.widthPx, theme: theme), l.fontPx, color, centerX: c,
                  top: centredTop(l.fontPx, dy: l.dyPx, theme: theme), clip: l.widthPx, theme: theme, in: &ctx)
        }
    }

    // MARK: Weather

    static func weather(_ look: KnobWeatherLook, _ draws: [KnobWeatherScene.Draw], _ tempC: Double?,
                        _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let w = theme.weather
        let c = theme.screen.diameterPx / 2
        let sky = CGRect(x: c - w.sky.widthPx / 2, y: w.sky.yPx, width: w.sky.widthPx, height: w.sky.heightPx)
        ctx.drawLayer { s in
            s.clip(to: Path(sky))
            s.translateBy(x: sky.minX, y: sky.minY)
            for d in draws { sprite(d, rayFrames: w.scene.rayFrames, in: &s) }
        }
        let text = tempC.map { "\(Int($0.rounded()))°" } ?? "--°"
        label(text, w.temp.fontPx, look.still ? w.temp.stillColor : w.temp.color, centerX: c, top: sky.maxY,
              clip: w.sky.widthPx, theme: theme, in: &ctx)
    }

    static func sprite(_ d: KnobWeatherScene.Draw, rayFrames: Int, in ctx: inout GraphicsContext) {
        let shape = KnobWeatherScene.shape(d.sprite, frame: d.frame, rayFrames: rayFrames)
        var c = ctx
        c.translateBy(x: d.x, y: d.y)
        guard let rgb = d.color else {
            var solid = Path()
            for (p, r) in shape.fillCircles { solid.addEllipse(in: CGRect(x: p.x - r, y: p.y - r, width: 2 * r, height: 2 * r)) }
            if let rect = shape.fillRect { solid.addRect(rect) }
            c.fill(solid, with: .color(.black))
            return
        }
        var path = Path()
        for s in shape.strokes { path.addPath(polyline(s)) }
        c.opacity = d.alpha
        c.stroke(path, with: .color(Color(rgb)),
                 style: StrokeStyle(lineWidth: 2 * shape.halfWidth, lineCap: .round, lineJoin: .round))
    }
}
