import SwiftUI

extension KnobFaceRender {
    static let tempRays: (image: CGImage, box: CGRect)? = {
        let r = KnobTempSky.rayRange, n = r.count
        var a = [UInt8](repeating: 0, count: n * n)
        var x0 = Int.max, y0 = Int.max, x1 = Int.min, y1 = Int.min
        for y in r {
            for x in r {
                let q = KnobTempSky.alpha8(KnobTempSky.rayAlpha(Float(x) + 0.5, Float(y) + 0.5))
                guard q > 0 else { continue }
                a[(y - r.lowerBound) * n + x - r.lowerBound] = q
                (x0, y0, x1, y1) = (min(x0, x), min(y0, y), max(x1, x), max(y1, y))
            }
        }
        guard x1 >= x0 else { return nil }
        let w = x1 - x0 + 1, h = y1 - y0 + 1, gold = KnobTempSky.rayColor
        var px = [UInt8](repeating: 0, count: w * h * 4)
        for y in 0..<h {
            for x in 0..<w {
                let q = Int(a[(y + y0 - r.lowerBound) * n + x + x0 - r.lowerBound]), i = (y * w + x) * 4
                func pre(_ c: UInt8) -> UInt8 { UInt8((Double(Int(c) * q) / 255).rounded()) }
                (px[i], px[i + 1], px[i + 2], px[i + 3]) = (pre(gold.r), pre(gold.g), pre(gold.b), UInt8(q))
            }
        }
        guard let data = CGDataProvider(data: Data(px) as CFData),
              let img = CGImage(width: w, height: h, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: w * 4,
                                space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedLast.rawValue),
                                provider: data, decode: nil, shouldInterpolate: false, intent: .defaultIntent) else { return nil }
        return (img, CGRect(x: x0, y: y0, width: w, height: h))
    }()

    static let tempCloudPaths: [[Path]] = KnobTempSky.bands.map { $0.clouds.map(skyCloud) }

    static func skyCloud(_ cl: KnobTempSky.Cloud) -> Path {
        guard let pts = KnobTempSky.shape(cl) else { return Path() }
        var p = Path()
        p.move(to: CGPoint(x: Double(pts[0].x), y: Double(pts[0].y)))
        for (k, q) in cl.puffs.enumerated() {
            let c = CGPoint(x: Double(q.x), y: Double(q.y))
            func angle(_ v: SIMD2<Float>) -> Angle { .radians(atan2(Double(v.y) - c.y, Double(v.x) - c.x)) }
            p.addArc(center: c, radius: Double(q.r), startAngle: angle(pts[k]), endAngle: angle(pts[k + 1]), clockwise: false)
        }
        p.closeSubpath()
        return p
    }

    static func rimMask(_ from: Float, in ctx: inout GraphicsContext) {
        let center = CGPoint(x: 233, y: 233), r = Double(KnobTempSky.rimR)
        ctx.clipToLayer {
            $0.fill(disc(center, r), with: .radialGradient(Gradient(stops: [.init(color: .white, location: 0),
                                                                             .init(color: .white, location: Double(from) / r),
                                                                             .init(color: .clear, location: 1)]),
                                                           center: center, startRadius: 0, endRadius: r))
        }
    }

    static func tempSkyBack(_ f: KnobTempFace, in ctx: inout GraphicsContext) {
        guard KnobTempSky.band(f) != nil else { return }
        if f.hero == .sun, let rays = tempRays { ctx.draw(Image(decorative: rays.image, scale: 1), in: rays.box) }
        guard f.stars else { return }
        for s in KnobTempSky.stars {
            let a = KnobTempSky.starAlpha * KnobTempSky.rim(s.x, s.y, KnobTempSky.heroFade)
            ctx.fill(disc(CGPoint(x: Double(s.x), y: Double(s.y)), Double(s.r)), with: .color(Color(KnobTempSky.starColor).opacity(Double(a))))
        }
    }

    static func tempSkyFront(_ f: KnobTempFace, t: Double, in ctx: inout GraphicsContext) {
        guard let band = KnobTempSky.band(f), let b = f.cloudBand else { return }
        let opa = KnobArrive.spriteOpacity(t)
        guard opa > 0 else { return }
        switch f.hero {
        case .sun: sunHero(opa, in: &ctx)
        case .moon, .moonSmall: moonHero(f, opa, in: &ctx)
        case .none: break
        }
        let drift = KnobTempDrift(band).offsets(t: t - KnobArrive.seconds), tone = KnobTempSky.tones[KnobTempSky.tone(f, band)]
        for (i, cl) in band.clouds.enumerated() {
            var c = ctx
            c.translateBy(x: Double(drift[i].dx), y: Double(drift[i].dy))
            rimMask(band.fadeFrom, in: &c)
            let p = tempCloudPaths[b][i]
            c.opacity = Double(opa) / 255
            c.drawLayer { g in
                g.fill(p, with: .color(Color(cl.back ? tone.back : tone.front)))
                g.stroke(p, with: .color(Color(tone.line)), style: StrokeStyle(lineWidth: 4.5, lineJoin: .round))
            }
        }
    }

    static func sunHero(_ opa: Int, in ctx: inout GraphicsContext) {
        let c = CGPoint(x: Double(KnobTempSky.sunC), y: Double(KnobTempSky.sunC)), core = Double(KnobTempSky.sunCore)
        let halo = Color(KnobTempSky.rayColor)
        var g = ctx
        rimMask(KnobTempSky.heroFade, in: &g)
        g.opacity = KnobTempSky.sunAlpha * Double(opa) / 255
        g.drawLayer { l in
            l.fill(disc(c, KnobTempSky.haloR), with: .radialGradient(Gradient(stops: [.init(color: halo.opacity(0.22), location: 0),
                                                                                     .init(color: halo.opacity(0.22), location: 0.65),
                                                                                     .init(color: halo.opacity(0), location: 1)]),
                                                                    center: c, startRadius: 0, endRadius: KnobTempSky.haloR))
            l.fill(disc(c, core), with: .radialGradient(Gradient(stops: [.init(color: Color(KnobTempSky.sunLight), location: 0),
                                                                        .init(color: halo, location: 0.6),
                                                                        .init(color: Color(KnobTempSky.sunEdge), location: 1)]),
                                                       center: c, startRadius: 0, endRadius: core))
            l.stroke(disc(c, core), with: .color(halo), lineWidth: 1.5)
        }
    }

    static func moonHero(_ f: KnobTempFace, _ opa: Int, in ctx: inout GraphicsContext) {
        let m = KnobTempSky.moon(f), c = CGPoint(x: Double(m.cx), y: Double(m.cy)), r = Double(m.r), rx = Double(m.rx)
        let glow = Color(KnobTempSky.moonGlow)
        let body = disc(c, r)
        let side = Path(CGRect(x: m.waxing ? c.x : c.x - r - 1, y: c.y - r - 1, width: r + 1, height: 2 * r + 2))
        let lens = CGRect(x: c.x - rx, y: c.y - r, width: 2 * rx, height: 2 * r)
        let ell = m.rx < 0.5 ? Path(lens) : Path(ellipseIn: lens)
        let light = GraphicsContext.Shading.radialGradient(Gradient(colors: [Color(KnobTempSky.moonLight), Color(KnobTempSky.moonShade)]),
                                                           center: CGPoint(x: Double(m.lightX), y: Double(m.lightY)), startRadius: 0,
                                                           endRadius: Double(m.lightR))
        var g = ctx
        rimMask(KnobTempSky.heroFade, in: &g)
        g.opacity = Double(opa) / 255
        g.drawLayer { l in
            l.fill(disc(c, Double(m.glowR)), with: .radialGradient(Gradient(stops: [.init(color: glow.opacity(Double(m.glow)), location: 0),
                                                                                   .init(color: glow.opacity(Double(m.glow)), location: 0.6),
                                                                                   .init(color: glow.opacity(0), location: 1)]),
                                                                  center: c, startRadius: 0, endRadius: Double(m.glowR)))
            l.fill(body, with: .color(.black))
            var lit = l
            lit.clip(to: body)
            lit.fill(side, with: light)
            lit.fill(ell, with: m.illum < 0.5 ? .color(.black) : light)
        }
    }
}
