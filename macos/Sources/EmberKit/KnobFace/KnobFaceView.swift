import SwiftUI

/// One frame of a knob page, as data.
public enum KnobFace: Sendable {
    case bot(pose: BotPose, mood: KnobMood)
    case pomodoro(KnobPomoFace)
    /// `tempC` nil prints "--°".
    case weather(look: KnobWeatherLook, draws: [KnobWeatherScene.Draw], tempC: Double?)
}

/// Draws a `KnobFace` on the knob's 466 px round screen, scaled to fit.
/// `brightness` (0…1) dims it like the panel's backlight, never to black.
public struct KnobFaceView: View {
    let face: KnobFace
    let theme: KnobTheme
    let brightness: Double

    public init(_ face: KnobFace, theme: KnobTheme = .standard, brightness: Double = 1) {
        self.face = face; self.theme = theme; self.brightness = brightness
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

    static func font(_ px: Double) -> Font { .system(size: px, weight: .medium) }

    static func label(_ s: String, _ px: Double, _ c: RGB, at p: CGPoint, in ctx: inout GraphicsContext) {
        guard !s.isEmpty else { return }
        ctx.draw(Text(verbatim: s).font(font(px)).foregroundStyle(Color(c)), at: p, anchor: .center)
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
        let shape = b == KnobTheme.standard.bot ? shapeCache : KnobBotShape(theme: b)
        let rimDY = -r * p.offsetY * b.hopScale
        func screen(_ q: CGPoint, lean: Bool) -> CGPoint {
            let by = p.offsetY * b.hopScale - (1 - p.scaleY) + p.scaleY * q.y
            return CGPoint(x: c + r * ((lean ? p.offsetX : 0) + p.scaleX * q.x), y: c - r * by)
        }
        let color = theme.moodColor(p.mood)
        let rim = color.scaled(p.mood == .idle || p.mood == .sleepy ? b.rimDimGain : 1)
        var outline = polyline(shape.ring(p.triangle >= 0.5 ? 1 : 0).map { screen($0, lean: false) })
        outline.closeSubpath()
        ctx.fill(outline, with: .color(Color(b.bodyColor)))
        ctx.drawLayer { glow in
            glow.addFilter(.shadow(color: Color(rim, opacity: 0.8), radius: b.rimPx * 1.5))
            glow.stroke(outline, with: .color(Color(rim)), style: StrokeStyle(lineWidth: b.rimPx, lineJoin: .round))
        }

        let k = r * (p.scaleX + p.scaleY) / 2
        for eye in KnobBotShape.eyes(p, scale: b.eyeScale) {
            ctx.stroke(polyline(eye.points.map { screen($0, lean: true) }), with: .color(Color(b.eyeColor)),
                       style: StrokeStyle(lineWidth: eye.width * k, lineCap: .round, lineJoin: .round))
        }

        if p.badge > 0.01 {
            let at = CGPoint(x: c + r * cos(.pi / 4) * b.badge.at, y: c - r * sin(.pi / 4) * b.badge.at + rimDY)
            func disc(_ rad: Double) -> Path {
                Path(ellipseIn: CGRect(x: at.x - rad, y: at.y - rad, width: 2 * rad, height: 2 * rad))
            }
            ctx.fill(disc(b.badge.gap * p.badge * r), with: .color(.black))
            ctx.fill(disc(b.badge.dot * p.badge * r), with: .color(Color(color)))
        }
        if mood.showsHost && (p.mood == .waiting || p.mood == .error) {
            label(mood.host, b.host.fontPx, color, at: CGPoint(x: c, y: c + b.host.y * r + rimDY), in: &ctx)
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
        label(f.round, t.round.fontPx, t.colors.textDim, at: CGPoint(x: c, y: c + t.round.dyPx), in: &ctx)
        label(f.time, t.time.fontPx, f.timeColor, at: CGPoint(x: c, y: c + t.time.dyPx), in: &ctx)
        label(f.phase, t.phase.fontPx, f.phaseColor, at: CGPoint(x: c, y: c + t.phase.dyPx), in: &ctx)
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
            for d in draws { sprite(d, in: &s) }
        }
        let text = tempC.map { "\(Int($0.rounded()))°" } ?? "--°"
        label(text, w.temp.fontPx, look.still ? w.temp.stillColor : w.temp.color,
              at: CGPoint(x: c, y: sky.maxY + w.temp.fontPx * 0.65), in: &ctx)
    }

    static func sprite(_ d: KnobWeatherScene.Draw, in ctx: inout GraphicsContext) {
        let shape = KnobWeatherScene.shape(d.sprite, frame: d.frame)
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
