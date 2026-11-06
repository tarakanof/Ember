import CoreText
import SwiftUI

public enum KnobPrintHero {
    public static let bodyW = 60.0, bodyH = 56.0
    public static let headY = -24.0
    public static let partTop = headY + 9
    public static let partMax = 48.0
    public static let layer = 3.0
    public static let headSweep = 20.0, headPark = 30.0

    public static let body = RGB(hex: "#262626")!
    public static let rod = RGB(hex: "#2E2E2E")!
    public static let bed = RGB(hex: "#5A5A5A")!
    public static let bedHeat = RGB(hex: "#555555")!
    public static let gantry = RGB(hex: "#8A8A8A")!
    public static let head = RGB(hex: "#D8D8D8")!
    public static let winTop = RGB(hex: "#0C0C0C")!
    public static let winBottom = RGB(hex: "#070707")!
    public static let winOff = RGB(hex: "#080808")!
    public static let light = RGB(hex: "#FFF3E0")!
    public static let stoppedLight = RGB(hex: "#333333")!

    static func lightColor(_ l: KnobPrintFace.Look, _ c: KnobTheme.Print.Colors) -> RGB {
        switch l {
        case .attention: c.wait
        case .error: c.err
        case .done: c.done
        case .none, .offline: RGB(r: 0, g: 0, b: 0)
        case .stopped: stoppedLight
        default: light
        }
    }

    static func showsPart(_ l: KnobPrintFace.Look) -> Bool {
        l != .heating && l != .offline && l != .none && l != .idle
    }

    public static func bedY(_ f: KnobPrintFace) -> Double {
        partTop + (showsPart(f.look) ? partMax * KnobPrintFace.clamp01(f.heroProgress) : 0)
    }
}

extension KnobFaceRender {
    static func printFace(_ f: KnobPrintFace, _ theme: KnobTheme, t: Double?, in ctx: inout GraphicsContext) {
        let p = theme.print
        printRing(f, p, in: &ctx)
        printHero(f, p, t: t, in: &ctx)
        printDots(p, in: &ctx)
        printText(f, theme, in: &ctx)
        if f.dimmed {
            let d = theme.screen.diameterPx
            ctx.fill(Path(ellipseIn: CGRect(x: 0, y: 0, width: d, height: d)), with: .color(.black.opacity(p.dimOpacity / 255)))
        }
    }

    static func printRing(_ f: KnobPrintFace, _ p: KnobTheme.Print, in ctx: inout GraphicsContext) {
        let r = p.ring, c = 233.0, center = CGPoint(x: c, y: c), w = 2 * r.halfWidthPx
        var track = Path()
        track.addArc(center: center, radius: r.radiusPx, startAngle: .zero, endAngle: .degrees(360), clockwise: false)
        ctx.stroke(track, with: .color(Color(p.colors.track)), lineWidth: w)
        let frac = KnobPrintFace.clamp01(f.ring)
        if frac >= 1 {
            ctx.stroke(track, with: .color(Color(f.ringColor)), lineWidth: w)
        } else if frac > 0 {
            var arc = Path()
            arc.addArc(center: center, radius: r.radiusPx, startAngle: .degrees(-90), endAngle: .degrees(-90 + 360 * frac),
                       clockwise: false)
            ctx.stroke(arc, with: .color(Color(f.ringColor)), style: StrokeStyle(lineWidth: w, lineCap: .round))
        }
        guard f.head else { return }
        let a = 2 * Double.pi * frac
        let at = CGPoint(x: c + r.radiusPx * sin(a), y: c - r.radiusPx * cos(a))
        var edge = ctx
        var annulus = Path(ellipseIn: CGRect(x: 0, y: 0, width: 2 * c, height: 2 * c))
        annulus.addEllipse(in: CGRect(x: c - r.innerPx, y: c - r.innerPx, width: 2 * r.innerPx, height: 2 * r.innerPx))
        edge.clip(to: annulus, style: FillStyle(eoFill: true))
        let reach = r.glowRadiusPx * 2
        let stops = stride(from: 0.0, through: reach, by: 1).map { d in
            Gradient.Stop(color: Color(f.ringColor, opacity: r.glowOpacity * 0.5 * erfc((d - r.glowRadiusPx) / (r.glowSigmaPx * 2.0.squareRoot()))),
                          location: d / reach)
        }
        edge.fill(Path(ellipseIn: CGRect(x: at.x - reach, y: at.y - reach, width: 2 * reach, height: 2 * reach)),
                  with: .radialGradient(Gradient(stops: stops), center: at, startRadius: 0, endRadius: reach))
        func disc(_ rad: Double) -> Path { Path(ellipseIn: CGRect(x: at.x - rad, y: at.y - rad, width: 2 * rad, height: 2 * rad)) }
        edge.fill(disc(r.headRadiusPx), with: .color(Color(f.ringColor)))
        edge.fill(disc(r.headRadiusPx - r.headStrokePx), with: .color(Color(r.headColor)))
    }

    static func printHero(_ f: KnobPrintFace, _ p: KnobTheme.Print, t: Double?, in ctx: inout GraphicsContext) {
        typealias H = KnobPrintHero
        let c = p.colors, l = f.look
        let off = l == .offline || l == .none, heat = l == .heating
        let pulse = KnobPrintFace.pulse(l, t: t)
        let lightCol = H.lightColor(l, c)
        var g = ctx
        g.translateBy(x: p.hero.cxPx, y: p.hero.cyPx)
        g.scaleBy(x: p.hero.scale, y: p.hero.scale)
        func rr(_ x: Double, _ y: Double, _ w: Double, _ h: Double, _ r: Double) -> Path {
            Path(roundedRect: CGRect(x: x, y: y, width: w, height: h), cornerRadius: r)
        }
        func rect(_ x: Double, _ y: Double, _ w: Double, _ h: Double) -> Path { Path(CGRect(x: x, y: y, width: w, height: h)) }

        let body = rr(-H.bodyW, -H.bodyH, 2 * H.bodyW, 2 * H.bodyH, 14)
        g.fill(body, with: .color(Color(H.body)))
        var top = g
        top.clip(to: body)
        top.fill(rect(-H.bodyW, -H.bodyH, 2 * H.bodyW, 14), with: .color(Color(c.accent)))

        let winTop = off ? H.winOff : KnobPrintFace.mix(H.winTop, lightCol, 0.18 * pulse)
        g.fill(rr(-48, -38, 96, 84, 8), with: .linearGradient(Gradient(colors: [Color(winTop), Color(H.winBottom)]),
                                                             startPoint: CGPoint(x: 0, y: -40), endPoint: CGPoint(x: 0, y: 46)))
        g.fill(rect(-44, -38, 3, 84), with: .color(Color(H.rod)))
        g.fill(rect(41, -38, 3, 84), with: .color(Color(H.rod)))

        let bedY = H.bedY(f), part = bedY - H.partTop
        if part > 0 {
            let fil = c.fil, dark = KnobPrintFace.mix(RGB(r: 0, g: 0, b: 0), c.fil, 0.85)
            var y = 0.0, i = 0
            while y < part {
                let h = min(H.layer, part - y)
                g.fill(rect(-22, bedY - y - h, 44, h), with: .color(Color(i % 2 == 1 ? fil : dark)))
                y += H.layer
                i += 1
            }
        }
        let bedCol = heat ? KnobPrintFace.mix(H.bedHeat, c.heat, f.heroHeat) : H.bed
        let nozzle = heat ? KnobPrintFace.mix(H.head, c.heat, f.heroHeat) : H.head
        g.fill(rect(-42, bedY, 84, 5), with: .color(Color(bedCol)))
        g.fill(rect(-46, H.headY - 6, 92, 3), with: .color(Color(H.gantry)))
        let hx = KnobPrintFace.headX(sweeps: f.sweeps, look: l, t: t)
        g.fill(rr(hx - 9, H.headY - 10, 18, 12, 3), with: .color(Color(H.head)))
        var tri = Path()
        tri.move(to: CGPoint(x: hx - 4, y: H.headY + 2))
        tri.addLine(to: CGPoint(x: hx + 4, y: H.headY + 2))
        tri.addLine(to: CGPoint(x: hx, y: H.headY + 8))
        tri.closeSubpath()
        g.fill(tri, with: .color(Color(nozzle)))
        if l == .attention || l == .error {
            var sign = rr(-3.2, -9, 6.4, 20, 3.2)
            sign.addEllipse(in: CGRect(x: -3.4, y: 17 - 3.4, width: 6.8, height: 6.8))
            g.fill(sign, with: .color(Color(lightCol, opacity: pulse)))
        }
    }

    static func printDotPoint(_ k: Int, _ d: KnobTheme.Print.Dots, center c: Double = 233) -> CGPoint {
        let a = Double.pi / 2 - (Double(k) - Double(d.faces - 1) / 2) * (d.pitchPx / d.radiusPx)
        return CGPoint(x: c + cos(a) * d.radiusPx, y: c + sin(a) * d.radiusPx)
    }

    static func printDots(_ p: KnobTheme.Print, in ctx: inout GraphicsContext) {
        let d = p.dots, r = d.dotRadiusPx
        for k in 0..<d.faces {
            let at = printDotPoint(k, d)
            ctx.fill(Path(ellipseIn: CGRect(x: at.x - r, y: at.y - r, width: 2 * r, height: 2 * r)),
                     with: .color(Color(k == 0 ? p.colors.dotOn : p.colors.dotOff)))
        }
    }

    static func printTop(_ px: Double, base: Double, theme: KnobTheme) -> Double {
        let m = theme.font.metrics(px)
        return base - (m.lineHeightPx - m.baselinePx)
    }

    static func printText(_ f: KnobPrintFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let p = theme.print, c = 233.0
        if !f.value.isEmpty {
            let v = p.value
            let vw = textWidth(f.value, v.fontPx, theme: theme)
            let uw = f.unit.isEmpty ? 0 : textWidth(f.unit, v.unitFontPx, theme: theme)
            let x0 = ((theme.screen.diameterPx - (vw + (f.unit.isEmpty ? 0 : v.unitGapPx) + uw)) / 2).rounded(.down)
            label(f.value, v.fontPx, f.valueColor, centerX: x0, anchor: 0, top: printTop(v.fontPx, base: v.basePx, theme: theme),
                  theme: theme, in: &ctx)
            label(f.unit, v.unitFontPx, f.unitColor, centerX: x0 + vw + v.unitGapPx, anchor: 0,
                  top: printTop(v.unitFontPx, base: v.basePx, theme: theme), theme: theme, in: &ctx)
        }
        let lines: [(String, KnobTheme.Print.Line, RGB, Double)] = [
            (f.word, p.word, f.wordColor, p.word.basePx),
            (f.sub, p.sub, f.subColor, f.word.isEmpty ? (p.sub.aloneBasePx ?? p.sub.basePx) : p.sub.basePx),
            (f.small, p.small, f.smallColor, p.small.basePx),
        ]
        for (text, line, color, base) in lines where !text.isEmpty {
            label(text, line.fontPx, color, centerX: c, top: printTop(line.fontPx, base: base, theme: theme), clip: line.widthPx,
                  theme: theme, in: &ctx)
        }
    }
}
