import CoreGraphics
import Foundation

public struct BotStyle: Sendable {
    public var body: CGColor
    public var eyes: CGColor?
    public var rim: CGColor?
    public var badge: CGColor?
    public var shadow: Bool
    public var fill: Double
    public var eyeScale: Double
    public var hopScale: Double
    public var eyeGrow: CGSize = .zero
    public var plate: CGColor?
    public var printTemplate = false
    public var printMark: CGColor? = BotStyle.printMarkColor

    public init(body: CGColor, eyes: CGColor?, rim: CGColor? = nil, badge: CGColor? = nil,
                shadow: Bool = false, fill: Double, eyeScale: Double = 1, hopScale: Double = 1,
                plate: CGColor? = nil) {
        self.body = body; self.eyes = eyes; self.rim = rim; self.badge = badge
        self.shadow = shadow; self.fill = fill; self.eyeScale = eyeScale; self.hopScale = hopScale
        self.plate = plate
    }

    public static func menuBar(tint: CGColor, template: Bool = false) -> BotStyle {
        var s = BotStyle(body: tint, eyes: nil, fill: 16.0 / 22, eyeScale: 1.25, hopScale: 0.5)
        s.eyeGrow = CGSize(width: 0.5 / 8, height: 0.5 / 8)
        s.printTemplate = template
        if template { s.printMark = nil }
        return s
    }

    public static func dock(badge: CGColor?) -> BotStyle {
        var s = BotStyle(body: CGColor(srgbRed: 0.04, green: 0.04, blue: 0.045, alpha: 1),
                         eyes: CGColor(gray: 1, alpha: 1),
                         badge: badge, shadow: true, fill: 0.62, hopScale: 0.6,
                         plate: CGColor(srgbRed: 0.98, green: 0.98, blue: 0.98, alpha: 1))
        s.eyeGrow = CGSize(width: 0.05, height: 0.07)
        return s
    }

    public func bodyRadius(side: Double) -> Double { side / 2 * fill }

    static let plateSize = 824.0 / 1024
    static let plateCorner = 0.225

    public static let prusaOrange = CGColor(srgbRed: 250.0 / 255, green: 104.0 / 255, blue: 49.0 / 255, alpha: 1)
    static let printMarkColor = CGColor(srgbRed: 0.07, green: 0.07, blue: 0.07, alpha: 1)
    static let templateShade = 0.35

    var printAccent: CGColor { printTemplate ? CGColor(gray: 0, alpha: 1) : Self.prusaOrange }
}

public enum BotRenderer {
    public static func draw(_ pose: BotPose, in ctx: CGContext, rect: CGRect, style: BotStyle) {
        let half = min(rect.width, rect.height) / 2
        let r = style.bodyRadius(side: 2 * half)
        let m = ctx.userSpaceToDeviceSpaceTransform
        let pixelRadius = r * abs(m.a * m.d - m.b * m.c).squareRoot()
        ctx.saveGState()
        defer { ctx.restoreGState() }

        if let plate = style.plate {
            let side = 2 * half * BotStyle.plateSize
            let box = CGRect(x: rect.midX - side / 2, y: rect.midY - side / 2, width: side, height: side)
            let shape = CGPath(roundedRect: box, cornerWidth: side * BotStyle.plateCorner,
                               cornerHeight: side * BotStyle.plateCorner, transform: nil)
            ctx.saveGState()
            ctx.setShadow(offset: CGSize(width: 0, height: -0.012 * side), blur: 0.03 * side,
                          color: CGColor(gray: 0, alpha: 0.3))
            ctx.addPath(shape)
            ctx.setFillColor(plate)
            ctx.fillPath()
            ctx.restoreGState()
            ctx.addPath(shape)
            ctx.clip()
        }

        if style.shadow {
            ctx.setShadow(offset: CGSize(width: 0, height: -0.04 * r), blur: 0.1 * r,
                          color: CGColor(gray: 0, alpha: 0.22))
        }
        ctx.translateBy(x: rect.midX, y: rect.midY - (style.plate == nil ? 0.12 * r : 0))
        ctx.scaleBy(x: r, y: r)
        ctx.translateBy(x: pose.offsetX, y: pose.offsetY * style.hopScale - (1 - pose.scaleY))
        ctx.scaleBy(x: pose.scaleX, y: pose.scaleY)

        ctx.beginTransparencyLayer(auxiliaryInfo: nil)
        let body = bodyPath(triangle: pose.triangle)
        ctx.addPath(body)
        ctx.setFillColor(style.body)
        ctx.fillPath()
        if let rim = style.rim {
            ctx.addPath(body)
            ctx.setStrokeColor(rim)
            ctx.setLineWidth(0.02)
            ctx.strokePath()
        }
        if pose.fill > 0 { drawFill(pose, body: body, pixelRadius: pixelRadius, style: style, in: ctx) }

        if let c = style.eyes { ctx.setFillColor(c); ctx.setStrokeColor(c) } else {
            ctx.setBlendMode(.clear)
        }
        ctx.saveGState()
        ctx.addPath(body)
        ctx.clip()
        drawEyes(pose, in: ctx, scale: style.eyeScale, grow: style.eyeGrow)
        ctx.restoreGState()
        ctx.setBlendMode(.normal)

        let badgeAt = CGPoint(x: cos(.pi / 4) * 0.98, y: sin(.pi / 4) * 0.98)
        let showPrintBadge = pose.printBadge > 0
        let showBadge = style.badge != nil && pose.badge > 0.01 && !showPrintBadge
        if showBadge || showPrintBadge {
            ctx.setBlendMode(.clear)
            if showBadge { ctx.fillEllipse(in: circle(badgeAt, 0.3 * pose.badge)) }
            if showPrintBadge { ctx.fillEllipse(in: circle(printBadgeAt, (printBadgeRadius + 0.08) * pose.printBadge)) }
            ctx.setBlendMode(.normal)
        }
        ctx.endTransparencyLayer()
        ctx.setShadow(offset: .zero, blur: 0, color: nil)
        if showBadge, let badge = style.badge {
            ctx.setFillColor(badge)
            ctx.fillEllipse(in: circle(badgeAt, 0.22 * pose.badge))
        }
        if showPrintBadge { drawPrintBadge(scale: pose.printBadge, style: style, in: ctx) }
        if pose.sparkle > 0 { drawSparkle(pose.sparkle, style: style, in: ctx) }
    }

    static let printRim = 0.12
    static let printBadgeAt = CGPoint(x: cos(.pi / 4) * 1.1, y: sin(.pi / 4) * 1.1)
    static let printBadgeRadius = 0.38
    static let sparkleAt = CGPoint(x: -0.8, y: 0.84)

    static func fillLevel(_ fill: Double, pixelRadius: Double) -> Double {
        -1 + BotPose.fillPixels(fill, radius: pixelRadius) / max(pixelRadius, 1)
    }

    static func drawFill(_ pose: BotPose, body: CGPath, pixelRadius: Double, style: BotStyle, in ctx: CGContext) {
        let level = fillLevel(pose.fill, pixelRadius: pixelRadius)
        guard level > -1 else { return }
        var inset = CGAffineTransform(scaleX: 1 - printRim, y: 1 - printRim)
        guard let inner = body.copy(using: &inset) else { return }
        ctx.saveGState()
        ctx.addPath(inner)
        ctx.clip()
        ctx.clip(to: CGRect(x: -2, y: -2, width: 4, height: level + 2))
        if style.printTemplate {
            ctx.setBlendMode(.copy)
            ctx.setFillColor(CGColor(gray: 0, alpha: BotStyle.templateShade))
        } else {
            ctx.setFillColor(BotStyle.prusaOrange)
        }
        ctx.fill(CGRect(x: -2, y: -2, width: 4, height: 4))
        ctx.restoreGState()
    }

    static func drawPrintBadge(scale k: Double, style: BotStyle, in ctx: CGContext) {
        let c = printBadgeAt, rb = printBadgeRadius * k
        ctx.saveGState()
        defer { ctx.restoreGState() }
        ctx.beginTransparencyLayer(auxiliaryInfo: nil)
        ctx.setFillColor(style.printAccent)
        ctx.fillEllipse(in: circle(c, rb))
        if let mark = style.printMark { ctx.setFillColor(mark) } else { ctx.setBlendMode(.clear) }
        let stem = CGRect(x: c.x - 0.13 * rb, y: c.y - 0.08 * rb, width: 0.26 * rb, height: 0.7 * rb)
        ctx.addPath(CGPath(roundedRect: stem, cornerWidth: 0.13 * rb, cornerHeight: 0.13 * rb, transform: nil))
        ctx.fillPath()
        ctx.fillEllipse(in: circle(CGPoint(x: c.x, y: c.y - 0.42 * rb), 0.16 * rb))
        ctx.endTransparencyLayer()
    }

    static func drawSparkle(_ u: Double, style: BotStyle, in ctx: CGContext) {
        let size = 0.34 * sin(.pi * min(max(u, 0), 1))
        guard size > 0.01 else { return }
        let c = sparkleAt, spin = 0.5 * u
        let star = CGMutablePath()
        for i in 0..<8 {
            let a = spin + Double(i) * .pi / 4
            let rad = i.isMultiple(of: 2) ? size : 0.28 * size
            let p = CGPoint(x: c.x + rad * cos(a), y: c.y + rad * sin(a))
            if i == 0 { star.move(to: p) } else { star.addLine(to: p) }
        }
        star.closeSubpath()
        ctx.saveGState()
        ctx.addPath(star)
        ctx.setFillColor(style.printAccent)
        ctx.fillPath()
        ctx.restoreGState()
    }

    static let ringPoints = 96

    static func bodyPath(triangle k: Double) -> CGPath {
        let pts: [CGPoint] = (0..<ringPoints).map { i in
            let a = Double(i) / Double(ringPoints) * 2 * .pi
            let rad = 1 + (triangleRadius(a) - 1) * k
            return CGPoint(x: rad * cos(a), y: rad * sin(a))
        }
        return smoothClosedPath(pts)
    }

    static func triangleRadius(_ a: Double) -> Double {
        let sector = 2 * Double.pi / 3
        var local = (a - .pi / 2).truncatingRemainder(dividingBy: sector)
        if local < 0 { local += sector }
        let flat = 0.62 / cos(local - sector / 2)
        return smoothMin(flat, 1.1, k: 0.32)
    }

    static func smoothMin(_ a: Double, _ b: Double, k: Double) -> Double {
        let h = max(k - abs(a - b), 0) / k
        return min(a, b) - h * h * k * 0.25
    }

    static func smoothClosedPath(_ p: [CGPoint]) -> CGPath {
        let path = CGMutablePath()
        let n = p.count
        path.move(to: p[0])
        for i in 0..<n {
            let p0 = p[(i - 1 + n) % n], p1 = p[i], p2 = p[(i + 1) % n], p3 = p[(i + 2) % n]
            path.addCurve(to: p2,
                          control1: CGPoint(x: p1.x + (p2.x - p0.x) / 6, y: p1.y + (p2.y - p0.y) / 6),
                          control2: CGPoint(x: p2.x - (p3.x - p1.x) / 6, y: p2.y - (p3.y - p1.y) / 6))
        }
        path.closeSubpath()
        return path
    }

    static func drawEyes(_ pose: BotPose, in ctx: CGContext, scale: Double, grow: CGSize = .zero) {
        let reach = 0.6 - 0.2 * pose.triangle
        var cx = pose.gazeX * reach
        var cy = pose.gazeY * reach - 0.12 * pose.triangle - 0.1 * pose.slump
        let limit = 0.52 - 0.15 * pose.triangle
        let d = hypot(cx, cy)
        if d > limit { cx *= limit / d; cy *= limit / d }
        let fx = (1 - 0.45 * cx * cx).squareRoot()
        let fy = (1 - 0.45 * cy * cy).squareRoot()

        let straight = 0.06
        let lean = min(max(abs(cx) - straight, 0) / (BotBehavior.rest.x * 0.6 - straight), 1)
        let sep = (pose.eyes == .round ? 0.5 : 0.44) * fx * (1 + (scale - 1) * 0.5)
        for (side, lid) in [(-1.0, pose.lidLeft), (1.0, pose.lidRight)] {
            let rise = pose.eyes == .dash ? 0.04 * side * lean : 0
            let c = CGPoint(x: cx + side * sep / 2, y: cy + rise)
            eye(pose.eyes, side: side, lid: lid, lean: lean, at: c, fx: fx * scale, fy: fy * scale, grow: grow, in: ctx)
        }
    }

    static func eye(_ kind: BotEyes, side: Double, lid: Double, lean: Double = 1, at c: CGPoint,
                    fx: Double, fy: Double, grow: CGSize = .zero, in ctx: CGContext) {
        let gw = grow.width, gh = grow.height * (1 - lid)
        func lerp(_ a: Double, _ b: Double) -> Double { a + (b - a) * lid }
        let deg = Double.pi / 180
        switch kind {
        case .dash:
            capsule(at: c, w: lerp(0.14, 0.24) * fx + gw, h: lerp(0.38, 0.06) * fy + gh,
                    angle: lerp(27, -6) * lean * deg, in: ctx)
        case .angry:
            capsule(at: c, w: lerp(0.13, 0.22) * fx + gw, h: lerp(0.3, 0.06) * fy + gh,
                    angle: lerp(55, 10) * deg * -side, in: ctx)
        case .round:
            ellipse(at: c, w: lerp(0.3, 0.34) * fx + gw, h: lerp(0.44, 0.05) * fy + gh, angle: 10 * lean * deg, in: ctx)
        case .happy:
            let w = 0.3 * fx + gw, h = lerp(0.16, 0.03) * fy + gh / 2
            let arc = CGMutablePath()
            arc.move(to: CGPoint(x: c.x - w / 2, y: c.y - h / 2))
            arc.addQuadCurve(to: CGPoint(x: c.x + w / 2, y: c.y - h / 2),
                             control: CGPoint(x: c.x, y: c.y + h * 1.5))
            ctx.addPath(arc)
            ctx.setLineWidth(0.11 * min(fx, fy) + gw / 2)
            ctx.setLineCap(.round)
            ctx.strokePath()
        }
    }

    static func capsule(at c: CGPoint, w: Double, h: Double, angle: Double, in ctx: CGContext) {
        let rect = CGRect(x: -w / 2, y: -h / 2, width: w, height: h)
        let rr = min(w, h) / 2
        var t = CGAffineTransform(translationX: c.x, y: c.y).rotated(by: angle)
        ctx.addPath(CGPath(roundedRect: rect, cornerWidth: rr, cornerHeight: rr, transform: &t))
        ctx.fillPath()
    }

    static func ellipse(at c: CGPoint, w: Double, h: Double, angle: Double, in ctx: CGContext) {
        var t = CGAffineTransform(translationX: c.x, y: c.y).rotated(by: angle)
        ctx.addPath(CGPath(ellipseIn: CGRect(x: -w / 2, y: -h / 2, width: w, height: h), transform: &t))
        ctx.fillPath()
    }

    static func circle(_ c: CGPoint, _ r: Double) -> CGRect {
        CGRect(x: c.x - r, y: c.y - r, width: 2 * r, height: 2 * r)
    }
}
