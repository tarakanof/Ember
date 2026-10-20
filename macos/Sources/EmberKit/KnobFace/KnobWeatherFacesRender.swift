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

    static let nearestPi = Float(Double.pi)

    static func gaugeMarkerPoint(_ fraction: Double, t: Double?, _ w: KnobTheme.Weather) -> CGPoint {
        let f = Float(fraction) * Float(KnobArrive.eased(t)), r = Float(w.ring.radiusPx)
        let a = (Float(w.ring.fromDeg) + Float(w.ring.sweepDeg) * f) * nearestPi / 180
        return CGPoint(x: Double(233 + r * sin(a)), y: Double(233 - r * cos(a)))
    }

    static func gaugeMarker(_ fraction: Double, t: Double?, _ w: KnobTheme.Weather, in ctx: inout GraphicsContext) {
        marker(at: gaugeMarkerPoint(fraction, t: t, w), w, in: &ctx)
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

    static func sub(_ markup: String, _ color: RGB, top: Double? = nil, opacity: Int = 255, theme: KnobTheme, in ctx: inout GraphicsContext) {
        guard opacity > 0 else { return }
        let s = theme.weather.sub
        var c = ctx
        if opacity < 255 { c.opacity = Double(opacity) / 255 }
        runs(KnobSubMarkup.runs(markup), s.fontPx, base: color, top: top ?? s.yPx, theme: theme, in: &c)
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

    static func rain(_ f: KnobRainFace, _ theme: KnobTheme, t: Double? = nil, in ctx: inout GraphicsContext) {
        let w = theme.weather
        faceDots(1, w, in: &ctx)
        if !f.dry { drops(f, w, k: t.map { KnobArrive.frame($0) } ?? 0, opacity: KnobArrive.spriteOpacity(t), in: &ctx) }
        umbrella(w.rain.umbrellaColor, in: &ctx)
        gauge(f.pop == nil ? nil : { f.ringColor($0, w) }, w, in: &ctx)
        let vc = f.valueColor(w)
        valueUnit(f.valueText(t: t), vc, unit: "%", unitColor: vc, theme: theme, in: &ctx)
        if f.pop != nil {
            ends("0%", w.rain.endColor, "100%", KnobRainFace.color(f.intensity, w.rain), theme: theme, in: &ctx)
            gaugeMarker(f.fraction, t: t, w, in: &ctx)
        }
        sub(f.sub, w.sub.color, opacity: KnobArrive.subOpacity(t), theme: theme, in: &ctx)
    }

    static func fade(_ box: CGRect, _ ramp: Double) -> GraphicsContext.Shading {
        .linearGradient(Gradient(stops: [.init(color: .clear, location: 0), .init(color: .white, location: ramp),
                                         .init(color: .white, location: 1 - ramp), .init(color: .clear, location: 1)]),
                        startPoint: CGPoint(x: box.midX, y: box.minY), endPoint: CGPoint(x: box.midX, y: box.maxY))
    }

    static func drops(_ f: KnobRainFace, _ w: KnobTheme.Weather, sheets: [KnobRainFace.Sheet] = KnobRainFace.sheets, k: Int = 0,
                      opacity: Int = 255,
                      in ctx: inout GraphicsContext) {
        let snow = f.kind == .snow
        let color = Color(snow ? w.rain.snowColor : KnobRainFace.color(f.intensity, w.rain))
        let tile = Double(KnobRainFace.sheetH), period = 2 * tile
        let fr = f.frame(k: k)
        for (s, sheet) in sheets.enumerated() {
            let top = sheet.box.minY + Double(KnobRainFace.sheetOffset(sheet, snow: snow, k: k))
            var clipped = ctx
            clipped.clip(to: Path(sheet.box))
            var c = clipped
            c.clipToLayer { $0.fill(Path(sheet.box), with: fade(sheet.box, KnobRainFace.ramp)) }
            if opacity < 255 { c.opacity = Double(opacity) / 255 }
            for d in sheet.drops where d.level <= f.intensity {
                for wrap in -2...1 {
                    let ay = top + d.y + Double(d.tile) * tile + Double(wrap) * period, by = ay + d.len, bx = d.x - d.slant
                    guard by + 6 >= sheet.box.minY, ay - 6 <= sheet.box.maxY else { continue }
                    if snow {
                        let cx = (d.x + bx) / 2, cy = (ay + by) / 2, r = d.op >= 0.8 ? 5.0 : 4.0
                        var p = Path()
                        for k in 0..<3 {
                            let a = Double(k) * .pi / 3
                            p.move(to: CGPoint(x: cx - r * cos(a), y: cy - r * sin(a)))
                            p.addLine(to: CGPoint(x: cx + r * cos(a), y: cy + r * sin(a)))
                        }
                        c.stroke(p, with: .color(color.opacity(d.op)), style: StrokeStyle(lineWidth: 2.5, lineCap: .round))
                    } else {
                        c.stroke(line(CGPoint(x: d.x, y: ay), CGPoint(x: bx, y: by)), with: .color(color.opacity(d.op)),
                                 style: StrokeStyle(lineWidth: 4, lineCap: .round))
                    }
                }
            }
            if s < fr.drips.count, let d = fr.drips[s] {
                clipped.fill(dripPaths[d.pose].offsetBy(dx: KnobRainFace.dripX[s], dy: 203 + Double(d.dy)),
                             with: .color(color.opacity(Double(d.opa * opacity / 255) / 255)))
            }
            if s == 2 {
                for (n, sp) in fr.splashes.enumerated() {
                    guard let sp else { continue }
                    splash(n, dy: Double(sp.dy), color: color.opacity(Double(sp.opa * opacity / 255) / 255), in: &clipped)
                }
            }
            if s == 2, fr.bolt { bolt(opacity: opacity, in: &clipped) }
        }
    }

    static let dripPaths: [Path] = KnobRainFace.dripScale.map { sc in
        let ts = (0...24).map { Double($0) / 24 }
        func y(_ t: Double) -> Double { let u = 1 - t; return 199 * u * u + 2 * u * t * 205 + 208 * t * t }
        func hw(_ t: Double) -> Double { 8 * t * (1 - t) }
        let side = ts.map { CGPoint(x: hw($0) * sc, y: (y($0) - 203) * sc) }
        return polyline(side + side.reversed().map { CGPoint(x: -$0.x, y: $0.y) })
    }

    static func splash(_ n: Int, dy: Double, color: Color, in ctx: inout GraphicsContext) {
        let d = KnobRainFace.splashSource(n), x = d.x, y = Double(KnobRainFace.canopyY(Float(d.x))) - 3 + dy
        var p = line(CGPoint(x: x - 5, y: y - 6), CGPoint(x: x, y: y))
        p.addLine(to: CGPoint(x: x + 5, y: y - 6))
        ctx.stroke(p, with: .color(color), style: StrokeStyle(lineWidth: 3, lineCap: .round, lineJoin: .round))
    }

    static let boltPath: Path = {
        let o = KnobRainFace.boltAt
        var p = polyline(KnobRainFace.bolt.map { CGPoint(x: o.x + $0.x * 0.8 + 1, y: o.y + $0.y * 0.8 + 1) })
        p.closeSubpath()
        return p
    }()

    static func bolt(opacity: Int, in ctx: inout GraphicsContext) {
        let c = Color(KnobRainFace.boltColor)
        faded(opacity, in: &ctx) { g in
            g.fill(boltPath, with: .color(c))
            g.stroke(boltPath, with: .color(c), style: StrokeStyle(lineWidth: 1.6, lineJoin: .round))
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

    static func cast(_ f: KnobCastFace, _ theme: KnobTheme, t time: Double? = nil, in ctx: inout GraphicsContext) {
        let w = theme.weather, t = w.cast, fr = f.frame(t: time)
        faceDots(2, w, in: &ctx)
        castClouds(f, w, frame: fr, in: &ctx)
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
        valueUnit(f.valueText(t: time), vc, unit: f.unit, unitColor: vc, theme: theme, in: &ctx)
        if f.have { marker(at: KnobCastFace.ringPoint(f.nowDeg), w, in: &ctx) }
        if let d = f.drop(t: time) { castDrop(d, t.color, in: &ctx) }
        sub(f.sub, w.sub.color, opacity: KnobArrive.subOpacity(time), theme: theme, in: &ctx)
        guard f.hasCount else { return }
        let subW = KnobSubMarkup.runs(f.sub).map { textWidth($0.text, w.sub.fontPx, theme: theme) }.reduce(0, +).rounded()
        let sm = theme.font.metrics(w.sub.fontPx), lm = theme.font.metrics(t.labelFontPx)
        let avoid = CGRect(x: 233 - (subW / 2).rounded(.down), y: w.sub.yPx, width: subW, height: sm.lineHeightPx)
        let lw = textWidth(f.label, t.labelFontPx, theme: theme).rounded()
        if fr.label > 0, let at = KnobCastFace.labelPlace(f.labelDeg, w: lw, h: lm.lineHeightPx, avoid: avoid, gap: t.labelGapPx) {
            var c = ctx
            c.opacity = Double(fr.label) / 255
            label(f.label, t.labelFontPx, t.color, centerX: at.x, anchor: 0, top: at.y, theme: theme, in: &c)
        }
    }

    static let dropTailPath: Path = {
        let p = KnobCastFace.dropTail
        func flip(_ q: CGPoint) -> CGPoint { CGPoint(x: q.x, y: -q.y) }
        var tail = Path()
        tail.move(to: p[0])
        tail.addCurve(to: p[3], control1: p[1], control2: p[2])
        tail.addLine(to: flip(p[3]))
        tail.addCurve(to: p[0], control1: flip(p[2]), control2: flip(p[1]))
        tail.closeSubpath()
        return tail
    }()

    static let dropHeadPath = disc(.zero, KnobCastFace.dropHead)

    static func castDrop(_ d: KnobCastFace.Drop, _ color: RGB, in ctx: inout GraphicsContext) {
        let at = KnobCastFace.ringPoint(d.deg)
        var c = ctx
        c.translateBy(x: at.x, y: at.y)
        c.rotate(by: .degrees(d.deg))
        faded(d.opa, in: &c) { g in
            let line = StrokeStyle(lineWidth: 2, lineJoin: .round)
            g.fill(dropTailPath, with: .color(Color(color)))
            g.fill(dropHeadPath, with: .color(Color(color)))
            var outside = g
            outside.clip(to: dropHeadPath, options: .inverse)
            outside.stroke(dropTailPath, with: .color(.black), style: line)
            outside = g
            outside.clip(to: dropTailPath, options: .inverse)
            outside.stroke(dropHeadPath, with: .color(.black), style: line)
        }
    }

    static let castCloudPaths: [[[Path]]] = KnobCastFace.clouds.map { sides in
        sides.map { side in
            side.clouds.map { skyCloud($0.sky) }
        }
    }

    static let dayCloudPartly = buildCloud(33, [(13, 28, 7), (21, 23, 8.5), (30, 28, 7)])
    static let dayCloudMid = buildCloud(30, [(10, 25, 7), (19, 20, 9), (29, 25, 7)])
    static let dayCloudHigh = buildCloud(24, [(10, 19, 7), (19, 14, 9), (29, 19, 7)])

    static func buildCloud(_ base: Double, _ circles: [(Double, Double, Double)]) -> DayCloud {
        var shapes = circles.map { Path(ellipseIn: CGRect(x: $0.0 - $0.2, y: $0.1 - $0.2, width: 2 * $0.2, height: 2 * $0.2)) }
        var floor = CGPoint(x: 0, y: base)
        if let first = circles.first, let last = circles.last {
            shapes.append(Path(CGRect(x: first.0, y: first.1, width: last.0 - first.0, height: base - first.1)))
            func edge(_ c: (Double, Double, Double), _ sign: Double) -> Double {
                let dy = base - c.1
                return c.0 + sign * (c.2 * c.2 > dy * dy ? (c.2 * c.2 - dy * dy).squareRoot() : 0)
            }
            floor = CGPoint(x: edge(first, -1), y: edge(last, 1))
        }
        return DayCloud(shapes: shapes, base: base, floorFrom: floor.x, floorTo: floor.y)
    }

    struct DayCloud: Equatable {
        let shapes: [Path]
        let base: Double
        let floorFrom: Double
        let floorTo: Double

        func draw(fill: Color, edge: Color, in ctx: inout GraphicsContext) {
            var c = ctx
            c.clip(to: Path(CGRect(x: -100, y: -100, width: 700, height: 100 + base)))
            for s in shapes { c.fill(s, with: .color(fill)) }
            let line = StrokeStyle(lineWidth: 2)
            for (i, s) in shapes.enumerated() {
                var o = c
                var others = Path()
                for (j, q) in shapes.enumerated() where j != i { others.addPath(q) }
                o.clip(to: others, options: .inverse)
                o.stroke(s, with: .color(edge), style: line)
            }
            var floor = Path()
            floor.move(to: CGPoint(x: floorFrom, y: base))
            floor.addLine(to: CGPoint(x: floorTo, y: base))
            ctx.stroke(floor, with: .color(edge), style: StrokeStyle(lineWidth: 2, lineCap: .round))
        }
    }

    static func castClouds(_ f: KnobCastFace, _ w: KnobTheme.Weather, frame fr: KnobCastFace.Frame, in ctx: inout GraphicsContext) {
        guard (1...3).contains(f.variant) else { return }
        let streak = Color(f.snow ? w.rain.snowColor : w.cast.color)
        for (side, s) in KnobCastFace.sheets[f.variant - 1].enumerated() {
            if !f.dry && fr.gate > 0 {
                var g = ctx
                g.opacity = Double(fr.gate) / 255
                castSheet(s, snow: f.snow, color: streak, k: fr.k, in: &g)
            }
            let sky = KnobCastFace.clouds[f.variant - 1][side], tone = KnobCastFace.tones[sky.tone]
            let opacity = fr.sprite * fr.gloom / 255
            guard opacity > 0 else { continue }
            var c = ctx
            c.translateBy(x: Double(fr.dx[side]), y: 0)
            rimMask(Float(sky.fadeFrom), in: &c)
            c.opacity = Double(opacity) / 255
            c.drawLayer { g in
                for (ci, cl) in sky.clouds.enumerated() {
                    let p = castCloudPaths[f.variant - 1][side][ci]
                    g.fill(p, with: .color(Color(cl.back ? tone.back : tone.front)))
                    g.stroke(p, with: .color(Color(tone.line)), style: StrokeStyle(lineWidth: 4.5, lineJoin: .round))
                }
            }
        }
    }

    static func faded(_ opacity: Int, in ctx: inout GraphicsContext, _ paint: (inout GraphicsContext) -> Void) {
        guard opacity < 255 else { return paint(&ctx) }
        guard opacity > 0 else { return }
        ctx.opacity = Double(opacity) / 255
        ctx.drawLayer { paint(&$0) }
    }

    static func castSheet(_ s: KnobCastFace.Sheet, snow: Bool, color: Color, k: Int = 0, in ctx: inout GraphicsContext) {
        let h = Double(s.h), period = 2 * h, top = s.box.minY + Double(KnobCastFace.sheetOffset(s, snow: snow, k: k))
        var c = ctx
        c.clip(to: Path(s.box))
        c.clipToLayer { $0.fill(Path(s.box), with: fade(s.box, KnobCastFace.ramp)) }
        for (t, streaks) in [s.a, s.b].enumerated() {
            for d in streaks {
                for wrap in -2...1 {
                    let ay = top + d.y + Double(t) * h + Double(wrap) * period, bx = d.x - d.slant
                    guard ay + d.len + 6 >= s.box.minY, ay - 6 <= s.box.maxY else { continue }
                    if snow {
                        c.fill(disc(CGPoint(x: (d.x + bx) / 2, y: ay + d.len / 2), s.width * 0.75), with: .color(color.opacity(d.op)))
                    } else {
                        c.stroke(line(CGPoint(x: d.x, y: ay), CGPoint(x: bx, y: ay + d.len)), with: .color(color.opacity(d.op)),
                                 style: StrokeStyle(lineWidth: s.width, lineCap: .round))
                    }
                }
            }
        }
    }

    static func wind(_ f: KnobWindFace, _ theme: KnobTheme, t time: Double? = nil, in ctx: inout GraphicsContext) {
        let w = theme.weather, t = w.wind
        faceDots(3, w, in: &ctx)
        let m = KnobWindFace.mount(level: f.level), px = m.x - KnobWindFace.arm, top = m.y - KnobWindFace.poleTop
        let base = KnobWindFace.baseY, pole = Color(t.poleColor)
        ctx.fill(Path(roundedRect: CGRect(x: px - 3.5, y: top, width: 7, height: base - 6 - top), cornerRadius: 3.5), with: .color(pole))
        ctx.fill(Path(roundedRect: CGRect(x: px, y: m.y - 3, width: KnobWindFace.arm, height: 6), cornerRadius: 3), with: .color(pole))
        ctx.fill(Path(roundedRect: CGRect(x: px - 24, y: base - 7, width: 48, height: 7), cornerRadius: 3.5), with: .color(pole))
        ctx.fill(disc(CGPoint(x: px, y: top), 5.5), with: .color(pole))
        let sock = f.valueColor(w, t: time), opa = KnobArrive.spriteOpacity(time)
        let fr = KnobWindFace.frame(level: f.level, k: f.have ? time.map { KnobArrive.frame($0) } ?? 0 : nil)
        for (r, s) in fr.streaks.enumerated() where s.opa * opa / 255 > 0 {
            let a = KnobWindFace.streakStart(level: f.level, row: r), len = Double(KnobWindFace.geo(level: f.level).rows[r].len)
            ctx.stroke(line(CGPoint(x: a.x + Double(s.dx), y: a.y), CGPoint(x: a.x + Double(s.dx) + len, y: a.y)),
                       with: .color(Color(t.streakColor).opacity(Double(s.opa * opa / 255) / 255)),
                       style: StrokeStyle(lineWidth: 4, lineCap: .round))
        }
        var sc = ctx
        faded(opa, in: &sc) { g in
            for (k, q) in KnobWindFace.sockQuads(level: f.level, pose: fr.pose).enumerated() {
                g.fill(polyline(q + [q[0]]), with: .color(Color(k % 2 == 1 ? KnobWindFace.dark(sock, t) : sock)))
            }
        }
        let outline = polyline(KnobWindFace.leafOutline())
        for l in fr.leaves where l.opa * opa / 255 > 0 {
            let half = Double(KnobWindFace.leafBox) / 2, deg = Double(l.rot) * 180 / Double(KnobWindFace.leafRots)
            let at = CGAffineTransform(rotationAngle: deg * .pi / 180)
                .concatenating(CGAffineTransform(translationX: Double(l.x) + half, y: Double(l.y) + half))
            ctx.fill(outline.applying(at), with: .color(Color(t.leafColor).opacity(Double(l.opa * opa / 255) / 255)))
        }
        gauge(f.have ? { f.ringColor($0, w) } : nil, w, in: &ctx)
        valueUnit(f.valueText(t: time), sock, unit: f.have ? "km/h" : nil, unitColor: t.unitColor, theme: theme, in: &ctx)
        if f.have {
            ends("0", KnobWindFace.color(0, t), "\(t.gaugeKmh)+", KnobWindFace.color(t.gaugeKmh, t), theme: theme, in: &ctx)
            gaugeMarker(f.fraction, t: time, w, in: &ctx)
        }
        sub(f.sub, w.sub.color, opacity: KnobArrive.subOpacity(time), theme: theme, in: &ctx)
    }

    static func days(_ f: KnobDaysFace, _ theme: KnobTheme, t time: Double? = nil, in ctx: inout GraphicsContext) {
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
        let fr = time.map { f.frame(t: KnobArrive.quantised($0, fps: KnobArrive.fps)) } ?? f.rest
        for (i, r) in f.rows.enumerated() {
            var row = ctx
            if fr.dy[i] != 0 { row.translateBy(x: 0, y: Double(fr.dy[i])) }
            faded(fr.opa[i], in: &row) { g in
                let cy = Double(KnobDaysFace.rowY(n, i, t))
                label(KnobDaysFace.name(r.wday), t.nameFontPx, t.nameColor, centerX: Double(c.dayX), anchor: 0,
                      top: cy - (nm.lineHeightPx / 2).rounded(.down), theme: theme, in: &g)
                dayIcon(r.icon, t, origin: CGPoint(x: Double(c.iconX), y: cy - Double(t.iconPx) / 2), in: &g)
                let tempTop = cy - (em.lineHeightPx / 2).rounded(.down)
                var lo = g
                lo.opacity = Double(t.loOpacity) / 255
                label(KnobTempFace.degrees(fr.lo[i]), e.fontPx, KnobTempFace.color(Double(fr.lo[i]), w.scale), centerX: Double(c.loX + c.loW),
                      anchor: 1, top: tempTop, theme: theme, in: &lo)
                label(KnobTempFace.degrees(fr.hi[i]), e.fontPx, KnobTempFace.color(Double(fr.hi[i]), w.scale), centerX: Double(c.hiX),
                      anchor: 0, top: tempTop, theme: theme, in: &g)
                let hw = Double(t.barHPx) / 2, bx = Double(c.barX)
                g.stroke(line(CGPoint(x: bx + hw, y: cy), CGPoint(x: bx + Double(c.barW) - hw, y: cy)),
                         with: .color(Color(t.trackColor)), style: StrokeStyle(lineWidth: Double(t.barHPx), lineCap: .round))
                let (s0, s1) = f.span(i, barW: c.barW, t)
                let lo0 = Double(r.lo), span = Double(r.hi - r.lo)
                let stops: [Gradient.Stop] = (0...8).map { (k: Int) -> Gradient.Stop in
                    let u = Double(k) / 8
                    return Gradient.Stop(color: Color(KnobTempFace.color(lo0 + span * u, w.scale)), location: u)
                }
                g.stroke(line(CGPoint(x: bx + s0 + hw, y: cy), CGPoint(x: bx + s1 - hw, y: cy)),
                         with: .linearGradient(Gradient(stops: stops), startPoint: CGPoint(x: bx + s0, y: cy),
                                               endPoint: CGPoint(x: bx + s1, y: cy)),
                         style: StrokeStyle(lineWidth: Double(t.barHPx), lineCap: .round))
                if let pop = r.pop {
                    label("\(pop)%", t.popFontPx, t.popColor, centerX: Double(c.iconX) + Double(t.iconPx) / 2,
                          top: cy + Double(t.popDyPx), theme: theme, in: &g)
                }
            }
        }
    }

    static func dayIcon(_ icon: KnobDaysFace.Icon, _ t: KnobTheme.Weather.Days, origin o: CGPoint, in ctx: inout GraphicsContext) {
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
        let (p, fill, edge): (DayCloud, RGB, RGB) = switch icon {
        case .partly: (dayCloudPartly, t.cloudWhiteFill, t.cloudWhiteEdge)
        case .cloud: (dayCloudMid, t.cloudLowFill, t.cloudLowEdge)
        case .storm: (dayCloudHigh, t.stormCloudFill, t.stormCloudEdge)
        default: (dayCloudHigh, t.cloudGreyFill, t.cloudGreyEdge)
        }
        p.draw(fill: Color(fill), edge: Color(edge), in: &c)
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
            c.fill(polyline(bolt + [bolt[0]]), with: .color(Color(t.stormBoltColor)))
        default: break
        }
    }

    static func sun(_ f: KnobSunFace, _ theme: KnobTheme, t time: Double? = nil, in ctx: inout GraphicsContext) {
        let w = theme.weather, t = w.sun
        faceDots(5, w, in: &ctx)
        let center = CGPoint(x: t.cxPx, y: t.cyPx)
        let fr = time.map { KnobSunFace.frame(t: KnobArrive.quantised($0, fps: KnobArrive.fps)) } ?? KnobSunFace.rest
        if f.hero {
            var clipped = ctx
            clipped.clip(to: Path(CGRect(x: 0, y: 0, width: 466, height: t.clipYPx)))
            var rays = clipped
            if fr.rays < 255 { rays.opacity = Double(fr.rays) / 255 }
            for k in 0..<13 where fr.rays > 0 {
                let a = (-168 + 13 * Double(k)) * .pi / 180, r1 = t.radiusPx + 12, r2 = k % 2 == 1 ? 130.0 : 170
                var c = rays
                func ga(_ r: Double) -> Double { r / 170 <= 0.3 ? 0.55 : max(0.55 * (1 - (r / 170 - 0.3) / 0.7), 0) }
                let p1 = CGPoint(x: center.x + r1 * cos(a), y: center.y + r1 * sin(a))
                let p2 = CGPoint(x: center.x + r2 * cos(a), y: center.y + r2 * sin(a))
                let gold = Color(t.goldColor)
                c.stroke(line(p1, p2), with: .linearGradient(Gradient(colors: [gold.opacity(ga(r1)), gold.opacity(ga(r2))]),
                                                             startPoint: p1, endPoint: p2),
                         style: StrokeStyle(lineWidth: k % 2 == 1 ? 3 : 3.5, lineCap: .round))
            }
            var c = clipped
            if fr.dy != 0 { c.translateBy(x: 0, y: Double(fr.dy)) }
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
        gauge(f.have ? { [stops = f.colorStops(t)] in f.ringColor($0, w, stops: stops) } : nil, w, in: &ctx)
        if f.have {
            for e in [KnobSunFace.Event.rise, .set] {
                let a = (w.ring.fromDeg + w.ring.sweepDeg * f.frac(f.t[e.rawValue])) * .pi / 180
                ctx.stroke(line(CGPoint(x: 233 + 199 * sin(a), y: 233 - 199 * cos(a)), CGPoint(x: 233 + 210 * sin(a), y: 233 - 210 * cos(a))),
                           with: .color(Color(t.horizonColor)), style: StrokeStyle(lineWidth: 2, lineCap: .round))
            }
            ends(f.rise, t.horizonColor, f.set, t.horizonColor, theme: theme, in: &ctx)
            if f.hasNow { gaugeMarker(f.fraction, t: time, w, in: &ctx) }
        }
        valueUnit(f.valueText(t: time), f.valueColor(w), unit: f.valueText == "--" ? nil : f.unit, unitColor: t.unitColor, theme: theme, in: &ctx)
        sub(f.sub, f.subColor, opacity: KnobArrive.subOpacity(time), theme: theme, in: &ctx)
    }

    static func air(_ f: KnobAirFace, _ theme: KnobTheme, t: Double? = nil, in ctx: inout GraphicsContext) {
        let w = theme.weather
        gauge(f.have ? { f.ringColor($0, w) } : nil, w, in: &ctx)
        airEnds(w, in: &ctx)
        if f.wilted { flowerWilted(in: &ctx) } else { flowerHealthy(in: &ctx) }
        if f.wilted, let t { fallingPetal(KnobAirFace.petal(k: KnobArrive.frame(t)), in: &ctx) }
        faceDots(6, w, in: &ctx)
        valueUnit(f.valueText(t: t), f.valueColor(w, t: t), unit: f.unit, unitColor: w.air.unitColor, theme: theme, in: &ctx)
        if f.have { gaugeMarker(f.fraction, t: t, w, in: &ctx) }
        sub(f.sub, f.subColor(w), opacity: KnobArrive.subOpacity(t), theme: theme, in: &ctx)
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

    static let petalFill = Color(RGB(r: 0xC9, g: 0x8A, b: 0x9A)), petalLine = Color(RGB(r: 0xD9, g: 0xA9, b: 0xB4))

    static func fallingPetal(_ p: KnobAirFace.Petal, in ctx: inout GraphicsContext) {
        guard p.opa > 0 else { return }
        let half = Double(KnobAirFace.petalBox) / 2, x = Double(p.x) + half, y = Double(p.y) + half
        let path = petal(x, y, 4.5, 8, rot: 36 + 10 * Double(p.rot), about: CGPoint(x: x, y: y))
        var c = ctx
        faded(p.opa, in: &c) { g in
            g.fill(path, with: .color(petalFill))
            g.stroke(path, with: .color(petalLine), lineWidth: 1.5)
        }
    }

    static func flowerWilted(in ctx: inout GraphicsContext) {
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
