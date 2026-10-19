import CoreText
import SwiftUI

extension KnobFaceRender {
    static func disc(_ p: CGPoint, _ r: Double) -> Path { Path(ellipseIn: CGRect(x: p.x - r, y: p.y - r, width: 2 * r, height: 2 * r)) }

    static func faceDots(_ lit: Int, _ w: KnobTheme.Weather, in ctx: inout GraphicsContext) {
        let c = 233.0
        for k in 0..<w.dots.faces {
            ctx.fill(disc(KnobTempFace.dotPoint(k, w.dots, center: c), w.dots.dotRadiusPx),
                     with: .color(Color(k == lit ? w.dots.onColor : w.dots.offColor)))
        }
    }

    static func gauge(_ color: ((Double) -> RGB)?, _ w: KnobTheme.Weather, in ctx: inout GraphicsContext) {
        let c = 233.0
        var arc = Path()
        arc.addArc(center: CGPoint(x: c, y: c), radius: w.ring.radiusPx, startAngle: .degrees(w.ring.fromDeg - 90),
                   endAngle: .degrees(w.ring.fromDeg - 90 + w.ring.sweepDeg), clockwise: false)
        let style = StrokeStyle(lineWidth: w.ring.widthPx, lineCap: .round)
        guard let color else {
            ctx.stroke(arc, with: .color(Color(w.ring.trackColor)), style: style)
            return
        }
        let sweep = w.ring.sweepDeg
        var stops = stride(from: 0.0, through: sweep, by: 2).map { s in
            Gradient.Stop(color: Color(color(s / sweep)), location: s / 360)
        }
        let gap = (sweep + 360) / 2 / 360
        stops += [.init(color: Color(color(1)), location: gap), .init(color: Color(color(0)), location: gap),
                  .init(color: Color(color(0)), location: 1)]
        ctx.stroke(arc, with: .conicGradient(Gradient(stops: stops), center: CGPoint(x: c, y: c),
                                             angle: .degrees(w.ring.fromDeg - 90)), style: style)
    }

    static func marker(at p: CGPoint, _ w: KnobTheme.Weather, in ctx: inout GraphicsContext) {
        let at = CGPoint(x: p.x.rounded(), y: p.y.rounded())
        ctx.fill(disc(at, w.marker.discRadiusPx), with: .color(.black))
        ctx.stroke(disc(at, w.marker.ringRadiusPx), with: .color(Color(w.marker.color)), lineWidth: w.marker.ringWidthPx)
    }

    static func gaugeMarker(_ fraction: Double, _ w: KnobTheme.Weather, in ctx: inout GraphicsContext) {
        marker(at: KnobTempFace.gaugePoint(fraction, w, center: 233), w, in: &ctx)
    }

    static func textWidth(_ s: String, _ px: Double, theme: KnobTheme) -> Double {
        let line = CTLineCreateWithAttributedString(NSAttributedString(string: s, attributes: [.font: ctFont(theme, px)]))
        return CTLineGetTypographicBounds(line, nil, nil, nil)
    }

    static func spacedWidth(_ s: String, _ v: KnobTheme.Weather.Value, theme: KnobTheme) -> Double {
        let ct = ctFont(theme, v.fontPx)
        let adv = s.map { ch in
            CTLineGetTypographicBounds(CTLineCreateWithAttributedString(NSAttributedString(string: String(ch), attributes: [.font: ct])),
                                       nil, nil, nil).rounded()
        }
        return adv.reduce(0, +) + v.letterSpacePx * Double(max(s.count - 1, 0))
    }

    static func valueUnit(_ value: String, _ color: RGB, unit: String?, unitColor: RGB, theme: KnobTheme,
                          in ctx: inout GraphicsContext) {
        let w = theme.weather, v = w.value, u = w.unit, c = 233.0
        let uw = unit.map { textWidth($0, u.fontPx, theme: theme).rounded() + u.gapPx } ?? 0
        let center = c - (uw / 2).rounded(.down)
        spacedLabel(value, v.fontPx, color, centerX: center, spacing: v.letterSpacePx, top: v.yPx, theme: theme, in: &ctx)
        guard let unit else { return }
        let right = center - (spacedWidth(value, v, theme: theme) / 2).rounded(.down) + spacedWidth(value, v, theme: theme)
        let vm = theme.font.metrics(v.fontPx), um = theme.font.metrics(u.fontPx)
        let baseline = v.yPx + vm.lineHeightPx - vm.baselinePx
        label(unit, u.fontPx, unitColor, centerX: right + u.gapPx, anchor: 0,
              top: baseline - (um.lineHeightPx - um.baselinePx), theme: theme, in: &ctx)
    }

    static func runs(_ runs: [KnobSubRun], _ px: Double, base: RGB, top: Double, theme: KnobTheme, in ctx: inout GraphicsContext) {
        let widths = runs.map { textWidth($0.text, px, theme: theme) }
        var x = 233 - widths.reduce(0, +) / 2
        for (r, wd) in zip(runs, widths) {
            label(r.text, px, r.color ?? base, centerX: x, anchor: 0, top: top, theme: theme, in: &ctx)
            x += wd
        }
    }

    static func sub(_ markup: String, _ color: RGB, top: Double? = nil, theme: KnobTheme, in ctx: inout GraphicsContext) {
        let s = theme.weather.sub
        runs(KnobSubMarkup.runs(markup), s.fontPx, base: color, top: top ?? s.yPx, theme: theme, in: &ctx)
    }

    static func ends(_ lo: String, _ loColor: RGB, _ hi: String, _ hiColor: RGB, theme: KnobTheme, in ctx: inout GraphicsContext) {
        let e = theme.weather.ends, d = theme.screen.diameterPx
        label(lo, e.fontPx, loColor, centerX: e.xPx, anchor: 0, top: e.yPx, theme: theme, in: &ctx)
        label(hi, e.fontPx, hiColor, centerX: d - e.xPx, anchor: 1, top: e.yPx, theme: theme, in: &ctx)
    }

    static func line(_ a: CGPoint, _ b: CGPoint) -> Path {
        var p = Path()
        p.move(to: a)
        p.addLine(to: b)
        return p
    }

    static func rain(_ f: KnobRainFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let w = theme.weather
        faceDots(1, w, in: &ctx)
        if !f.dry { drops(f, w, in: &ctx) }
        umbrella(w.rain.umbrellaColor, in: &ctx)
        gauge(f.pop == nil ? nil : { f.ringColor($0, w) }, w, in: &ctx)
        let vc = f.valueColor(w)
        valueUnit(f.valueText, vc, unit: "%", unitColor: vc, theme: theme, in: &ctx)
        if f.pop != nil {
            ends("0%", w.rain.endColor, "100%", KnobRainFace.color(f.intensity, w.rain), theme: theme, in: &ctx)
            gaugeMarker(f.fraction, w, in: &ctx)
        }
        sub(f.sub, w.sub.color, theme: theme, in: &ctx)
    }

    static let rainSheets: [(box: CGRect, drops: [(Double, Double, Double, Double, Double, Int, Int)])] = [
        (CGRect(x: 58, y: 150, width: 60, height: 182), [
            (70, 12, 16, 0.9, 3, 0, 1), (92, 70, 12, 0.6, 3, 0, 2), (64, 118, 18, 0.8, 5, 0, 1), (86, 30, 14, 0.75, 3, 1, 1),
            (66, 88, 16, 1.0, 3, 1, 2), (96, 128, 12, 0.55, 4, 1, 1), (82, 42, 14, 0.7, 3, 0, 3), (100, 104, 12, 0.6, 3, 0, 3),
            (100, 4, 12, 0.65, 3, 1, 3), (78, 62, 14, 0.7, 4, 1, 3)]),
        (CGRect(x: 348, y: 150, width: 54, height: 182), [
            (376, 20, 16, 0.85, 3, 0, 1), (396, 84, 12, 0.6, 4, 0, 2), (370, 126, 14, 0.95, 3, 0, 1), (390, 8, 14, 0.7, 3, 1, 1),
            (372, 60, 18, 0.9, 5, 1, 2), (398, 110, 12, 0.55, 3, 1, 1), (386, 50, 14, 0.7, 3, 0, 3), (358, 96, 12, 0.6, 3, 0, 3),
            (358, 34, 12, 0.65, 3, 1, 3), (384, 136, 14, 0.7, 4, 1, 3)]),
        (CGRect(x: 150, y: 46, width: 166, height: 86), [
            (172, 10, 16, 0.9, 3, 0, 1), (246, 64, 14, 0.7, 3, 0, 2), (298, 112, 12, 0.8, 4, 0, 1), (205, 128, 16, 0.6, 3, 0, 2),
            (226, 24, 14, 1.0, 3, 1, 1), (186, 80, 12, 0.65, 5, 1, 2), (280, 40, 16, 0.8, 3, 1, 1), (160, 136, 14, 0.7, 3, 1, 1),
            (212, 36, 14, 0.7, 3, 0, 3), (270, 92, 12, 0.6, 3, 0, 3), (256, 108, 12, 0.65, 4, 1, 3), (300, 70, 14, 0.7, 3, 1, 3)]),
    ]

    static func drops(_ f: KnobRainFace, _ w: KnobTheme.Weather, in ctx: inout GraphicsContext) {
        let snow = f.kind == .snow
        let color = Color(snow ? w.rain.snowColor : KnobRainFace.color(f.intensity, w.rain))
        for sheet in rainSheets {
            var c = ctx
            c.clip(to: Path(sheet.box))
            for (x, y, len, op, slant, tile, level) in sheet.drops where level <= f.intensity {
                let ay = sheet.box.minY + y + Double(tile) * 150, by = ay + len, bx = x - slant
                if snow {
                    let cx = (x + bx) / 2, cy = (ay + by) / 2, r = op >= 0.8 ? 5.0 : 4.0
                    var p = Path()
                    for k in 0..<3 {
                        let a = Double(k) * .pi / 3
                        p.move(to: CGPoint(x: cx - r * cos(a), y: cy - r * sin(a)))
                        p.addLine(to: CGPoint(x: cx + r * cos(a), y: cy + r * sin(a)))
                    }
                    c.stroke(p, with: .color(color.opacity(op)), style: StrokeStyle(lineWidth: 2.5, lineCap: .round))
                } else {
                    c.stroke(line(CGPoint(x: x, y: ay), CGPoint(x: bx, y: by)), with: .color(color.opacity(op)),
                             style: StrokeStyle(lineWidth: 4, lineCap: .round))
                }
            }
        }
    }

    static func umbrella(_ color: RGB, in ctx: inout GraphicsContext) {
        let l = 105.0, r = 361.0, rim = 198.0, ry = 103.0, cx = (l + r) / 2, rx = (r - l) / 2, n = 5
        var canopy = Path()
        canopy.move(to: CGPoint(x: l, y: rim))
        for k in 0...90 {
            let a = Double.pi - Double(k) * .pi / 90
            canopy.addLine(to: CGPoint(x: cx + rx * cos(a), y: rim - ry * sin(a)))
        }
        let sw = (r - l) / Double(n)
        for k in (0..<n).reversed() {
            let sc = l + sw * (Double(k) + 0.5)
            for j in 0...24 {
                let a = Double(j) * .pi / 24
                canopy.addLine(to: CGPoint(x: sc + sw / 2 * cos(a), y: rim - 9 * sin(a)))
            }
        }
        canopy.closeSubpath()
        let style = StrokeStyle(lineWidth: 4.5, lineCap: .round, lineJoin: .round)
        let c = Color(color)
        ctx.fill(canopy, with: .color(.black))
        ctx.stroke(canopy, with: .color(c), style: style)
        var h = line(CGPoint(x: 233, y: 185), CGPoint(x: 233, y: 210.75))
        h.addPath(line(CGPoint(x: 233, y: 336.25), CGPoint(x: 233, y: 366)))
        h.addArc(center: CGPoint(x: 244, y: 366), radius: 11, startAngle: .degrees(180), endAngle: .degrees(0), clockwise: true)
        h.addPath(line(CGPoint(x: 233, y: 81), CGPoint(x: 233, y: 93)))
        ctx.stroke(h, with: .color(c), style: style)
    }

    static func cast(_ f: KnobCastFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let w = theme.weather, t = w.cast
        faceDots(2, w, in: &ctx)
        castClouds(f, w, in: &ctx)
        let center = CGPoint(x: 233, y: 233), style = StrokeStyle(lineWidth: w.ring.widthPx, lineCap: .butt)
        func arc(_ a: Double, _ b: Double) -> Path {
            var p = Path()
            p.addArc(center: center, radius: w.ring.radiusPx, startAngle: .degrees(a - 90), endAngle: .degrees(b - 90),
                     clockwise: false)
            return p
        }
        for (lo, hi) in KnobCastFace.cells() { ctx.stroke(arc(lo, hi), with: .color(Color(t.cellColor)), style: style) }
        for s in f.segments { ctx.stroke(arc(s.from, s.to), with: .color(Color(KnobRainFace.color(s.intensity, w.rain))), style: style) }
        let vc = f.valueColor(w)
        valueUnit(f.valueText, vc, unit: f.unit, unitColor: vc, theme: theme, in: &ctx)
        if f.have { marker(at: KnobCastFace.ringPoint(f.nowDeg), w, in: &ctx) }
        sub(f.sub, w.sub.color, theme: theme, in: &ctx)
        guard f.hasCount else { return }
        let subW = KnobSubMarkup.runs(f.sub).map { textWidth($0.text, w.sub.fontPx, theme: theme) }.reduce(0, +).rounded()
        let sm = theme.font.metrics(w.sub.fontPx), lm = theme.font.metrics(t.labelFontPx)
        let avoid = CGRect(x: 233 - (subW / 2).rounded(.down), y: w.sub.yPx, width: subW, height: sm.lineHeightPx)
        let lw = textWidth(f.label, t.labelFontPx, theme: theme).rounded()
        if let at = KnobCastFace.labelPlace(f.labelDeg, w: lw, h: lm.lineHeightPx, avoid: avoid, gap: t.labelGapPx) {
            label(f.label, t.labelFontPx, t.color, centerX: at.x, anchor: 0, top: at.y, theme: theme, in: &ctx)
        }
    }

    static let castClouds: [[(tone: Int, clouds: [(back: Bool, base: Double, circles: [(Double, Double, Double)])])]] = [
        [(0, [(false, 170, [(168.5, 156.5, 19.5), (203, 140, 27), (245, 134, 28.5), (291.5, 152, 22.5)])])],
        [(1, [(false, 180, [(63.5, 161.1, 29.7), (114.8, 144.9, 35.1), (160.7, 163.8, 21.6)])]),
         (1, [(false, 172, [(305.3, 155.8, 21.6), (351.2, 136.9, 35.1), (402.5, 153.1, 29.7)])])],
        [(2, [(true, 132, [(20, 116, 28), (66, 96, 32), (118, 102, 30), (166, 118, 22)]),
              (false, 172, [(30, 154, 30), (80, 138, 34), (134, 144, 30), (186, 158, 24)])]),
         (2, [(true, 122, [(300, 108, 22), (348, 92, 30), (400, 86, 32), (446, 106, 28)]),
              (false, 162, [(264, 148, 24), (314, 134, 30), (368, 128, 34), (420, 144, 30)])])],
    ]

    static let cloudTones: [(front: RGB, back: RGB, line: RGB)] = [
        (RGB(r: 0xE6, g: 0xEC, b: 0xF2), RGB(r: 0xE6, g: 0xEC, b: 0xF2), RGB(r: 0xC4, g: 0xCC, b: 0xD4)),
        (RGB(r: 0x8E, g: 0x96, b: 0xA0), RGB(r: 0x73, g: 0x7B, b: 0x86), RGB(r: 0xA8, g: 0xB0, b: 0xBA)),
        (RGB(r: 0x55, g: 0x5D, b: 0x68), RGB(r: 0x3A, g: 0x40, b: 0x48), RGB(r: 0x6A, g: 0x74, b: 0x80)),
    ]

    static func cloud(_ base: Double, _ circles: [(Double, Double, Double)]) -> Path {
        var p = Path()
        for (x, y, r) in circles { p.addEllipse(in: CGRect(x: x - r, y: y - r, width: 2 * r, height: 2 * r)) }
        if let first = circles.first, let last = circles.last {
            p.addRect(CGRect(x: first.0, y: first.1, width: last.0 - first.0, height: base - first.1))
        }
        let below = Path(CGRect(x: -100, y: base, width: 700, height: 600))
        return p.subtracting(below)
    }

    static func castClouds(_ f: KnobCastFace, _ w: KnobTheme.Weather, in ctx: inout GraphicsContext) {
        guard (1...3).contains(f.variant) else { return }
        let snow = f.snow, streak = Color(snow ? w.rain.snowColor : w.cast.color)
        if !f.dry {
            for s in castSheets[f.variant - 1] {
                var c = ctx
                c.clip(to: Path(s.box))
                for (x, y, len, op, slant) in s.streaks {
                    let ay = s.box.minY + y, bx = x - slant
                    if snow {
                        c.fill(disc(CGPoint(x: (x + bx) / 2, y: ay + len / 2), s.width * 0.75), with: .color(streak.opacity(op)))
                    } else {
                        c.stroke(line(CGPoint(x: x, y: ay), CGPoint(x: bx, y: ay + len)), with: .color(streak.opacity(op)),
                                 style: StrokeStyle(lineWidth: s.width, lineCap: .round))
                    }
                }
            }
        }
        for side in castClouds[f.variant - 1] {
            let tone = cloudTones[side.tone]
            for cl in side.clouds {
                let p = cloud(cl.base, cl.circles)
                ctx.fill(p, with: .color(Color(cl.back ? tone.back : tone.front)))
                ctx.stroke(p, with: .color(Color(tone.line)), style: StrokeStyle(lineWidth: 4.5, lineJoin: .round))
            }
        }
    }

    static let castSheets: [[(box: CGRect, width: Double, streaks: [(Double, Double, Double, Double, Double)])]] = [
        [(CGRect(x: 160, y: 172, width: 142, height: 36), 3,
          [(176, 6, 5, 0.8, 0), (214, 22, 5, 1, 0), (248, 10, 5, 0.7, 0), (282, 30, 5, 0.9, 0), (196, 34, 5, 0.6, 0)])],
        [(CGRect(x: 54, y: 182, width: 118, height: 26), 4,
          [(72, 4, 10, 0.85, 3), (118, 17, 10, 1, 3), (150, 29, 10, 0.7, 3), (94, 35, 10, 0.9, 4), (132, 9, 10, 0.75, 3), (60, 22, 10, 0.8, 3)]),
         (CGRect(x: 298, y: 174, width: 108, height: 34), 4,
          [(312, 7, 10, 0.9, 3), (356, 20, 10, 0.75, 3), (392, 32, 10, 1, 4), (334, 36, 10, 0.6, 3), (376, 2, 10, 0.85, 3), (306, 24, 10, 0.75, 3)])],
        [(CGRect(x: 58, y: 172, width: 142, height: 36), 4.5,
          [(70, 4, 16, 0.9, 6), (102, 22, 16, 1, 6), (138, 10, 16, 0.75, 6), (170, 34, 16, 0.95, 6), (196, 48, 14, 0.7, 5)]),
         (CGRect(x: 248, y: 164, width: 152, height: 44), 4.5,
          [(262, 8, 16, 0.9, 6), (296, 30, 16, 1, 6), (330, 4, 16, 0.75, 6), (362, 26, 16, 0.95, 6), (394, 44, 14, 0.7, 5)])],
    ]

    static func wind(_ f: KnobWindFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let w = theme.weather, t = w.wind
        faceDots(3, w, in: &ctx)
        let m = KnobWindFace.mount(level: f.level), px = m.x - KnobWindFace.arm, top = m.y - KnobWindFace.poleTop
        let base = KnobWindFace.baseY, pole = Color(t.poleColor)
        ctx.fill(Path(roundedRect: CGRect(x: px - 3.5, y: top, width: 7, height: base - 6 - top), cornerRadius: 3.5), with: .color(pole))
        ctx.fill(Path(roundedRect: CGRect(x: px, y: m.y - 3, width: KnobWindFace.arm, height: 6), cornerRadius: 3), with: .color(pole))
        ctx.fill(Path(roundedRect: CGRect(x: px - 24, y: base - 7, width: 48, height: 7), cornerRadius: 3.5), with: .color(pole))
        ctx.fill(disc(CGPoint(x: px, y: top), 5.5), with: .color(pole))
        let sock = f.valueColor(w)
        for (k, q) in KnobWindFace.sockQuads(level: f.level).enumerated() {
            ctx.fill(polyline(q + [q[0]]), with: .color(Color(k % 2 == 1 ? KnobWindFace.dark(sock, t) : sock)))
        }
        gauge(f.have ? { f.ringColor($0, w) } : nil, w, in: &ctx)
        valueUnit(f.valueText, sock, unit: f.have ? "km/h" : nil, unitColor: t.unitColor, theme: theme, in: &ctx)
        if f.have {
            ends("0", KnobWindFace.color(0, t), "\(t.gaugeKmh)+", KnobWindFace.color(t.gaugeKmh, t), theme: theme, in: &ctx)
            gaugeMarker(f.fraction, w, in: &ctx)
        }
        sub(f.sub, w.sub.color, theme: theme, in: &ctx)
    }

    static func days(_ f: KnobDaysFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let w = theme.weather, t = w.days
        faceDots(4, w, in: &ctx)
        var ring = Path()
        ring.addEllipse(in: CGRect(x: 233 - w.ring.radiusPx, y: 233 - w.ring.radiusPx, width: 2 * w.ring.radiusPx,
                                   height: 2 * w.ring.radiusPx))
        ctx.stroke(ring, with: .color(Color(t.trackColor)), lineWidth: t.ringWidthPx)
        guard f.have else {
            valueUnit("--", w.stillColor, unit: nil, unitColor: w.stillColor, theme: theme, in: &ctx)
            return
        }
        let c = f.cols(t), n = f.rows.count, e = w.ends
        let nm = theme.font.metrics(t.nameFontPx), em = theme.font.metrics(e.fontPx)
        for (i, r) in f.rows.enumerated() {
            let cy = Double(KnobDaysFace.rowY(n, i, t))
            label(KnobDaysFace.name(r.wday), t.nameFontPx, t.nameColor, centerX: Double(c.dayX), anchor: 0,
                  top: cy - (nm.lineHeightPx / 2).rounded(.down), theme: theme, in: &ctx)
            dayIcon(r.icon, origin: CGPoint(x: Double(c.iconX), y: cy - Double(t.iconPx) / 2), in: &ctx)
            let tempTop = cy - (em.lineHeightPx / 2).rounded(.down)
            var lo = ctx
            lo.opacity = Double(t.loOpacity) / 255
            label(KnobTempFace.degrees(r.lo), e.fontPx, KnobTempFace.color(Double(r.lo), w.scale), centerX: Double(c.loX + c.loW),
                  anchor: 1, top: tempTop, theme: theme, in: &lo)
            label(KnobTempFace.degrees(r.hi), e.fontPx, KnobTempFace.color(Double(r.hi), w.scale), centerX: Double(c.hiX), anchor: 0,
                  top: tempTop, theme: theme, in: &ctx)
            let hw = Double(t.barHPx) / 2, bx = Double(c.barX)
            ctx.stroke(line(CGPoint(x: bx + hw, y: cy), CGPoint(x: bx + Double(c.barW) - hw, y: cy)),
                       with: .color(Color(t.trackColor)), style: StrokeStyle(lineWidth: Double(t.barHPx), lineCap: .round))
            let (s0, s1) = f.span(i, barW: c.barW, t)
            let lo0 = Double(r.lo), span = Double(r.hi - r.lo)
            let stops: [Gradient.Stop] = (0...8).map { (k: Int) -> Gradient.Stop in
                let u = Double(k) / 8
                return Gradient.Stop(color: Color(KnobTempFace.color(lo0 + span * u, w.scale)), location: u)
            }
            ctx.stroke(line(CGPoint(x: bx + s0 + hw, y: cy), CGPoint(x: bx + s1 - hw, y: cy)),
                       with: .linearGradient(Gradient(stops: stops), startPoint: CGPoint(x: bx + s0, y: cy),
                                             endPoint: CGPoint(x: bx + s1, y: cy)),
                       style: StrokeStyle(lineWidth: Double(t.barHPx), lineCap: .round))
            if let pop = r.pop {
                label("\(pop)%", t.popFontPx, t.popColor, centerX: Double(c.iconX) + Double(t.iconPx) / 2,
                      top: cy + Double(t.popDyPx), theme: theme, in: &ctx)
            }
        }
    }

    static func dayIcon(_ icon: KnobDaysFace.Icon, origin o: CGPoint, in ctx: inout GraphicsContext) {
        var c = ctx
        c.translateBy(x: o.x, y: o.y)
        let gold = Color(RGB(r: 0xFF, g: 0xB5, b: 0x47))
        if icon == .sun {
            for k in 0..<8 {
                let a = Double(k) * .pi / 4
                c.stroke(line(CGPoint(x: 20 + 14.5 * sin(a), y: 20 - 14.5 * cos(a)), CGPoint(x: 20 + 18 * sin(a), y: 20 - 18 * cos(a))),
                         with: .color(gold), style: StrokeStyle(lineWidth: 2.5, lineCap: .round))
            }
            c.fill(disc(CGPoint(x: 20, y: 20), 11), with: .color(Color(RGB(r: 0xFF, g: 0xD0, b: 0x60))))
            return
        }
        if icon == .partly { c.fill(disc(CGPoint(x: 14, y: 14), 9), with: .color(Color(RGB(r: 0xFF, g: 0xD0, b: 0x60)))) }
        if icon == .rain {
            for k in 0..<3 {
                c.stroke(line(CGPoint(x: 13 + 7 * Double(k), y: 28), CGPoint(x: 11 + 7 * Double(k), y: 34)),
                         with: .color(Color(RGB(r: 0x5C, g: 0x9C, b: 0xE0))), style: StrokeStyle(lineWidth: 2.5, lineCap: .round))
            }
        }
        let (base, circles, fill, edge): (Double, [(Double, Double, Double)], RGB, RGB) = switch icon {
        case .partly: (33, [(13, 28, 7), (21, 23, 8.5), (30, 28, 7)], RGB(r: 0xE6, g: 0xEC, b: 0xF2), RGB(r: 0xC4, g: 0xCC, b: 0xD4))
        case .cloud: (30, [(10, 25, 7), (19, 20, 9), (29, 25, 7)], RGB(r: 0x8E, g: 0x96, b: 0xA0), RGB(r: 0xA8, g: 0xB0, b: 0xBA))
        case .storm: (24, [(10, 19, 7), (19, 14, 9), (29, 19, 7)], RGB(r: 0x55, g: 0x5D, b: 0x68), RGB(r: 0x6A, g: 0x74, b: 0x80))
        default: (24, [(10, 19, 7), (19, 14, 9), (29, 19, 7)], RGB(r: 0x8E, g: 0x96, b: 0xA0), RGB(r: 0xA8, g: 0xB0, b: 0xBA))
        }
        let p = cloud(base, circles)
        c.fill(p, with: .color(Color(fill)))
        c.stroke(p, with: .color(Color(edge)), lineWidth: 2)
        switch icon {
        case .fog:
            let mist = Color(RGB(r: 0x9A, g: 0xA4, b: 0xB0)), st = StrokeStyle(lineWidth: 2.5, lineCap: .round)
            c.stroke(line(CGPoint(x: 9, y: 29), CGPoint(x: 31, y: 29)), with: .color(mist), style: st)
            c.stroke(line(CGPoint(x: 13, y: 34), CGPoint(x: 27, y: 34)), with: .color(mist), style: st)
        case .snow:
            for k in 0..<3 {
                c.fill(disc(CGPoint(x: 13 + 7 * Double(k), y: k == 1 ? 34 : 30), 2), with: .color(Color(RGB(r: 0xD0, g: 0xDC, b: 0xE8))))
            }
        case .storm:
            let bolt = [CGPoint(x: 21, y: 24), CGPoint(x: 15, y: 32), CGPoint(x: 19.5, y: 32), CGPoint(x: 17, y: 39),
                        CGPoint(x: 25, y: 30), CGPoint(x: 20.5, y: 30), CGPoint(x: 23, y: 24)]
            c.fill(polyline(bolt + [bolt[0]]), with: .color(Color(RGB(r: 0xF0, g: 0xD0, b: 0x40))))
        default: break
        }
    }

    static func sun(_ f: KnobSunFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let w = theme.weather, t = w.sun
        faceDots(5, w, in: &ctx)
        let center = CGPoint(x: t.cxPx, y: t.cyPx)
        if f.hero {
            for k in 0..<13 {
                let a = (-168 + 13 * Double(k)) * .pi / 180, r1 = t.radiusPx + 12, r2 = k % 2 == 1 ? 130.0 : 170
                var c = ctx
                c.clip(to: Path(CGRect(x: 0, y: 0, width: 466, height: t.clipYPx)))
                func ga(_ r: Double) -> Double { r / 170 <= 0.3 ? 0.55 : max(0.55 * (1 - (r / 170 - 0.3) / 0.7), 0) }
                let p1 = CGPoint(x: center.x + r1 * cos(a), y: center.y + r1 * sin(a))
                let p2 = CGPoint(x: center.x + r2 * cos(a), y: center.y + r2 * sin(a))
                let gold = Color(t.goldColor)
                c.stroke(line(p1, p2), with: .linearGradient(Gradient(colors: [gold.opacity(ga(r1)), gold.opacity(ga(r2))]),
                                                             startPoint: p1, endPoint: p2),
                         style: StrokeStyle(lineWidth: k % 2 == 1 ? 3 : 3.5, lineCap: .round))
            }
            var c = ctx
            c.clip(to: Path(CGRect(x: 0, y: 0, width: 466, height: t.clipYPx)))
            c.fill(disc(center, 74), with: .color(Color(t.goldColor).opacity(0.12)))
            c.fill(disc(center, t.radiusPx), with: .radialGradient(Gradient(colors: [Color(RGB(r: 0xFF, g: 0xE0, b: 0x7A)),
                                                                                      Color(t.goldColor), Color(RGB(r: 0xF0, g: 0x8A, b: 0x3A))]),
                                                                  center: center, startRadius: 0, endRadius: t.radiusPx * 1.2))
            c.stroke(disc(center, t.radiusPx), with: .color(Color(t.goldColor)), lineWidth: 1.5)
        }
        ctx.stroke(line(CGPoint(x: 125, y: t.horizonYPx), CGPoint(x: 341, y: t.horizonYPx)), with: .color(Color(RGB(r: 0x9A, g: 0xA4, b: 0xB0))),
                   style: StrokeStyle(lineWidth: 4.5, lineCap: .round))
        for (a, b) in [(168.0, 208.0), (258.0, 298.0)] {
            ctx.stroke(line(CGPoint(x: a, y: t.horizonYPx + 8), CGPoint(x: b, y: t.horizonYPx + 8)),
                       with: .color(Color(w.stillColor)), style: StrokeStyle(lineWidth: 3, lineCap: .round))
        }
        gauge(f.have ? { f.ringColor($0, w) } : nil, w, in: &ctx)
        if f.have {
            for e in [KnobSunFace.Event.rise, .set] {
                let a = (w.ring.fromDeg + w.ring.sweepDeg * f.frac(f.t[e.rawValue])) * .pi / 180
                ctx.stroke(line(CGPoint(x: 233 + 199 * sin(a), y: 233 - 199 * cos(a)), CGPoint(x: 233 + 210 * sin(a), y: 233 - 210 * cos(a))),
                           with: .color(Color(t.horizonColor)), style: StrokeStyle(lineWidth: 2, lineCap: .round))
            }
            ends(f.rise, t.horizonColor, f.set, t.horizonColor, theme: theme, in: &ctx)
            if f.hasNow { gaugeMarker(f.fraction, w, in: &ctx) }
        }
        valueUnit(f.valueText, f.valueColor(w), unit: f.valueText == "--" ? nil : f.unit, unitColor: t.unitColor, theme: theme, in: &ctx)
        sub(f.sub, f.subColor, theme: theme, in: &ctx)
    }

    static func air(_ f: KnobAirFace, _ theme: KnobTheme, in ctx: inout GraphicsContext) {
        let w = theme.weather
        gauge(f.have ? { f.ringColor($0, w) } : nil, w, in: &ctx)
        airEnds(w, in: &ctx)
        if f.wilted { flowerWilted(in: &ctx) } else { flowerHealthy(in: &ctx) }
        faceDots(6, w, in: &ctx)
        valueUnit(f.valueText, f.valueColor(w), unit: f.unit, unitColor: w.air.unitColor, theme: theme, in: &ctx)
        if f.have { gaugeMarker(f.fraction, w, in: &ctx) }
        sub(f.sub, f.subColor(w), theme: theme, in: &ctx)
    }

    static func petal(_ cx: Double, _ cy: Double, _ rx: Double, _ ry: Double, rot: Double, about p: CGPoint) -> Path {
        Path(ellipseIn: CGRect(x: cx - rx, y: cy - ry, width: 2 * rx, height: 2 * ry))
            .applying(CGAffineTransform(translationX: -p.x, y: -p.y).concatenating(CGAffineTransform(rotationAngle: rot * .pi / 180))
                .concatenating(CGAffineTransform(translationX: p.x, y: p.y)))
    }

    static func leaf(_ x: Double, _ y: Double, deg: Double, len: Double, wid: Double) -> Path {
        var p = Path()
        p.move(to: .zero)
        p.addQuadCurve(to: CGPoint(x: len, y: 0), control: CGPoint(x: len / 2, y: -0.75 * wid))
        p.addQuadCurve(to: .zero, control: CGPoint(x: len / 2, y: 0.75 * wid))
        return p.applying(CGAffineTransform(rotationAngle: deg * .pi / 180).concatenating(CGAffineTransform(translationX: x, y: y)))
    }

    static func flowerHealthy(in ctx: inout GraphicsContext) {
        var stem = Path()
        stem.move(to: CGPoint(x: 233, y: 204))
        stem.addCurve(to: CGPoint(x: 233, y: 126), control1: CGPoint(x: 230, y: 176), control2: CGPoint(x: 237, y: 150))
        ctx.stroke(stem, with: .color(Color(RGB(r: 0x4E, g: 0x8F, b: 0x45))), lineWidth: 5)
        for (x, y, d, l, wd) in [(233.0, 188.0, -22.0, 40.0, 20.0), (233, 170, -158, 34, 18)] {
            let p = leaf(x, y, deg: d, len: l, wid: wd)
            ctx.fill(p, with: .color(Color(RGB(r: 0x7B, g: 0xC8, b: 0x6C))))
            ctx.stroke(p, with: .color(Color(RGB(r: 0x5F, g: 0xAE, b: 0x55))), lineWidth: 3)
        }
        for k in 0..<6 {
            let p = petal(233, 93, 12.5, 20, rot: 60 * Double(k), about: CGPoint(x: 233, y: 112))
            ctx.fill(p, with: .color(Color(RGB(r: 0xF5, g: 0x9A, b: 0xB0))))
            ctx.stroke(p, with: .color(Color(RGB(r: 0xF4, g: 0xB6, b: 0xC6))), lineWidth: 2.5)
        }
        ctx.fill(disc(CGPoint(x: 233, y: 112), 11), with: .color(Color(RGB(r: 0xF5, g: 0xC8, b: 0x4A))))
        ctx.stroke(disc(CGPoint(x: 233, y: 112), 11), with: .color(Color(RGB(r: 0xE3, g: 0xAE, b: 0x36))), lineWidth: 2)
    }

    static func flowerWilted(in ctx: inout GraphicsContext) {
        let petalFill = Color(RGB(r: 0xC9, g: 0x8A, b: 0x9A)), petalLine = Color(RGB(r: 0xD9, g: 0xA9, b: 0xB4))
        ctx.stroke(line(CGPoint(x: 178, y: 206), CGPoint(x: 292, y: 206)), with: .color(Color(RGB(r: 0x2E, g: 0x32, b: 0x36))), lineWidth: 2)
        for (x, y, d) in [(188.0, 202.0, 72.0), (266, 203, 104), (284, 201, 60)] {
            let p = petal(x, y, 4.5, 8, rot: d, about: CGPoint(x: x, y: y))
            ctx.fill(p, with: .color(petalFill))
            ctx.stroke(p, with: .color(petalLine), lineWidth: 1.5)
        }
        var c = ctx
        c.translateBy(x: 226, y: 204)
        c.rotate(by: .degrees(18))
        c.translateBy(x: -226, y: -204)
        var stem = Path()
        stem.move(to: CGPoint(x: 226, y: 204))
        stem.addCurve(to: CGPoint(x: 226, y: 132), control1: CGPoint(x: 222, y: 176), control2: CGPoint(x: 230, y: 150))
        c.stroke(stem, with: .color(Color(RGB(r: 0x7C, g: 0x8A, b: 0x50))), lineWidth: 5)
        for (x, y, d, l, wd) in [(226.0, 188.0, 20.0, 36.0, 17.0), (226, 172, 165, 30, 15)] {
            let p = leaf(x, y, deg: d, len: l, wid: wd)
            c.fill(p, with: .color(Color(RGB(r: 0x9C, g: 0xA3, b: 0x5A))))
            c.stroke(p, with: .color(Color(RGB(r: 0x7F, g: 0x86, b: 0x46))), lineWidth: 3)
        }
        c.translateBy(x: 226, y: 122)
        c.rotate(by: .degrees(32))
        c.translateBy(x: -226, y: -122)
        for k in [0, 2, 3, 5] {
            let p = petal(226, 104, 11.5, 18, rot: 60 * Double(k), about: CGPoint(x: 226, y: 122))
            c.fill(p, with: .color(petalFill))
            c.stroke(p, with: .color(petalLine), lineWidth: 2.5)
        }
        c.fill(disc(CGPoint(x: 226, y: 122), 10), with: .color(Color(RGB(r: 0xC9, g: 0xA8, b: 0x4A))))
        c.stroke(disc(CGPoint(x: 226, y: 122), 10), with: .color(Color(RGB(r: 0xA9, g: 0x8A, b: 0x3A))), lineWidth: 2)
        for (x, y, r) in [(166.0, 128.0, 3.0), (182, 166, 3.5), (196, 108, 2.5), (302, 176, 3), (276, 90, 2.5), (156, 188, 2.5)] {
            ctx.fill(disc(CGPoint(x: x, y: y), r), with: .color(Color(RGB(r: 0x8A, g: 0x8A, b: 0x8A)).opacity(0.6)))
        }
        let red = Color(RGB(r: 0xFF, g: 0x50, b: 0x50))
        let sign = polyline([CGPoint(x: 318, y: 108), CGPoint(x: 340, y: 146), CGPoint(x: 296, y: 146), CGPoint(x: 318, y: 108)])
        ctx.fill(sign, with: .color(.black))
        ctx.stroke(sign, with: .color(red), style: StrokeStyle(lineWidth: 4, lineJoin: .round))
        ctx.stroke(line(CGPoint(x: 318, y: 121), CGPoint(x: 318, y: 133)), with: .color(red), style: StrokeStyle(lineWidth: 4, lineCap: .round))
        ctx.fill(disc(CGPoint(x: 318, y: 139), 2.5), with: .color(red))
    }

    static func airEnds(_ w: KnobTheme.Weather, in ctx: inout GraphicsContext) {
        let good = Color(w.air.bandColors[0]), bad = Color(w.air.bandColors[4])
        var fl = ctx
        fl.translateBy(x: 116, y: 383)
        fl.scaleBy(x: 1.2, y: 1.2)
        fl.translateBy(x: -116, y: -383)
        for k in 0..<5 {
            fl.stroke(petal(116, 376, 3.5, 6, rot: 72 * Double(k), about: CGPoint(x: 116, y: 383)), with: .color(good), lineWidth: 2.5)
        }
        fl.fill(disc(CGPoint(x: 116, y: 383), 3), with: .color(Color(w.air.stopColors[2])))
        var hz = ctx
        hz.translateBy(x: 349, y: 383)
        hz.scaleBy(x: 1.2, y: 1.2)
        hz.translateBy(x: -352, y: -386)
        hz.stroke(polyline([CGPoint(x: 352, y: 368), CGPoint(x: 370, y: 399), CGPoint(x: 334, y: 399), CGPoint(x: 352, y: 368)]),
                  with: .color(bad), style: StrokeStyle(lineWidth: 3.5, lineJoin: .round))
        hz.stroke(line(CGPoint(x: 352, y: 378), CGPoint(x: 352, y: 388)), with: .color(bad), style: StrokeStyle(lineWidth: 3.5, lineCap: .round))
        hz.fill(disc(CGPoint(x: 352, y: 394), 2), with: .color(bad))
    }
}
