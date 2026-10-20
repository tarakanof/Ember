import SwiftUI

extension KnobFaceRender {
    static func sunRaysDirect(_ t: KnobTheme.Weather.Sun, opacity: Double, in ctx: inout GraphicsContext) {
        let center = CGPoint(x: t.cxPx, y: t.cyPx)
        var rays = ctx
        rays.clip(to: Path(CGRect(x: 0, y: 0, width: 466, height: t.clipYPx)))
        if opacity < 1 { rays.opacity = opacity }
        for k in 0..<13 {
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
    }

    static let sunRaysRaster: (image: CGImage, box: CGRect, sun: KnobTheme.Weather.Sun)? = MainActor.assumeIsolated { () -> (image: CGImage, box: CGRect, sun: KnobTheme.Weather.Sun)? in
        let sun = KnobTheme.standard.weather.sun
        let r = ImageRenderer(content: Canvas { ctx, _ in sunRaysDirect(sun, opacity: 1, in: &ctx) }.frame(width: 466, height: 466))
        r.scale = 1
        guard let full = r.cgImage, full.width == 466, full.height == 466 else { return nil }
        var px = [UInt8](repeating: 0, count: 466 * 466 * 4)
        let drew = px.withUnsafeMutableBytes { buf -> Bool in
            guard let c = CGContext(data: buf.baseAddress, width: 466, height: 466, bitsPerComponent: 8, bytesPerRow: 466 * 4,
                                    space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                    bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            c.draw(full, in: CGRect(x: 0, y: 0, width: 466, height: 466))
            return true
        }
        guard drew else { return nil }
        var (x0, y0, x1, y1) = (Int.max, Int.max, Int.min, Int.min)
        for y in 0..<466 {
            for x in 0..<466 where px[(y * 466 + x) * 4 + 3] > 0 {
                (x0, y0, x1, y1) = (min(x0, x), min(y0, y), max(x1, x), max(y1, y))
            }
        }
        guard x1 >= x0, let img = full.cropping(to: CGRect(x: x0, y: y0, width: x1 - x0 + 1, height: y1 - y0 + 1)) else { return nil }
        return (img, CGRect(x: x0, y: y0, width: x1 - x0 + 1, height: y1 - y0 + 1), sun)
    }

    static func sunRays(_ t: KnobTheme.Weather.Sun, opacity: Double, in ctx: inout GraphicsContext) {
        guard let cached = sunRaysRaster, cached.sun == t else { return sunRaysDirect(t, opacity: opacity, in: &ctx) }
        var c = ctx
        if opacity < 1 { c.opacity = opacity }
        c.draw(Image(decorative: cached.image, scale: 1), in: cached.box)
    }
}
