import CoreText
import SwiftUI

/// One frame of a knob page, as data.
public enum KnobFace: Sendable, Equatable {
    case bot(pose: BotPose, mood: KnobMood)
    case pomodoro(KnobPomoFace)
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
            case .bot(let pose, let mood): KnobFaceRender.bot(pose, mood, theme, in: &ctx)
            case .pomodoro(let p): KnobFaceRender.pomodoro(p, theme, in: &ctx)
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

    static func bot(_ p: BotPose, _ mood: KnobMood, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
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
        if mood.showsHost && (p.mood == .waiting || p.mood == .error) {
            let lh = theme.font.metrics(b.host.fontPx).lineHeightPx
            let top = (c + b.host.y * r).rounded() - (lh / 2).rounded(.down) + rimDY
            label(mood.host, b.host.fontPx, color, centerX: c, top: top, clip: b.host.widthPx, theme: theme, in: &ctx)
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
