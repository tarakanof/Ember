import CoreGraphics
import Foundation

/// How a `BotPose` is painted: one style for the menu bar, one for the Dock.
public struct BotStyle: Sendable {
    public var body: CGColor
    /// nil cuts the eyes out of the body (transparent), which keeps a menu-bar
    /// image usable as a template.
    public var eyes: CGColor?
    public var rim: CGColor?
    public var badge: CGColor?
    public var shadow: Bool
    /// Body radius as a fraction of half the canvas; the rest is hop headroom.
    public var fill: Double
    /// Eyes read too thin at 18 pt, so the menu bar draws them larger.
    public var eyeScale: Double
    public var hopScale: Double

    public init(body: CGColor, eyes: CGColor?, rim: CGColor? = nil, badge: CGColor? = nil,
                shadow: Bool = false, fill: Double, eyeScale: Double = 1, hopScale: Double = 1) {
        self.body = body; self.eyes = eyes; self.rim = rim; self.badge = badge
        self.shadow = shadow; self.fill = fill; self.eyeScale = eyeScale; self.hopScale = hopScale
    }

    public static func menuBar(tint: CGColor) -> BotStyle {
        BotStyle(body: tint, eyes: nil, fill: 0.78, eyeScale: 1.3, hopScale: 0.5)
    }

    public static func dock(badge: CGColor?) -> BotStyle {
        BotStyle(body: CGColor(srgbRed: 0.04, green: 0.04, blue: 0.045, alpha: 1),
                 eyes: CGColor(gray: 1, alpha: 1),
                 rim: CGColor(gray: 1, alpha: 0.14),
                 badge: badge, shadow: true, fill: 0.72)
    }
}

/// Draws the bot in code — a 96-point body ring (so shapes morph point by point)
/// plus capsule/oval/arc eyes. Coordinates are y-up.
public enum BotRenderer {
    public static func draw(_ pose: BotPose, in ctx: CGContext, rect: CGRect, style: BotStyle) {
        let half = min(rect.width, rect.height) / 2
        let r = half * style.fill
        ctx.saveGState()
        defer { ctx.restoreGState() }

        if style.shadow {
            ctx.setShadow(offset: CGSize(width: 0, height: -0.05 * r), blur: 0.14 * r,
                          color: CGColor(gray: 0, alpha: 0.35))
        }
        ctx.translateBy(x: rect.midX, y: rect.midY - 0.12 * r)
        ctx.scaleBy(x: r, y: r)
        // Squash pivots on the ground, not the centre.
        ctx.translateBy(x: pose.offsetX, y: pose.offsetY * style.hopScale - (1 - pose.scaleY))
        ctx.scaleBy(x: pose.scaleX, y: pose.scaleY)

        ctx.beginTransparencyLayer(auxiliaryInfo: nil)
        let body = bodyPath(triangle: pose.triangle, slump: pose.slump)
        ctx.addPath(body)
        ctx.setFillColor(style.body)
        ctx.fillPath()
        if let rim = style.rim {
            ctx.addPath(body)
            ctx.setStrokeColor(rim)
            ctx.setLineWidth(0.02)
            ctx.strokePath()
        }

        if let c = style.eyes { ctx.setFillColor(c); ctx.setStrokeColor(c) } else {
            ctx.setBlendMode(.clear)
        }
        drawEyes(pose, in: ctx, scale: style.eyeScale)
        ctx.setBlendMode(.normal)

        let badgeAt = CGPoint(x: cos(.pi / 4) * 0.98, y: sin(.pi / 4) * 0.98)
        let showBadge = style.badge != nil && pose.badge > 0.01
        if showBadge {
            // Gap ring: cut from the body so the badge reads as sitting on top.
            ctx.setBlendMode(.clear)
            ctx.fillEllipse(in: circle(badgeAt, 0.3 * pose.badge))
            ctx.setBlendMode(.normal)
        }
        ctx.endTransparencyLayer()
        if showBadge, let badge = style.badge {
            ctx.setShadow(offset: .zero, blur: 0, color: nil)
            ctx.setFillColor(badge)
            ctx.fillEllipse(in: circle(badgeAt, 0.22 * pose.badge))
        }
    }

    // MARK: - Body

    static let ringPoints = 96

    /// Sphere ↔ rounded triangle, blended per ring point; `slump` sags it.
    static func bodyPath(triangle k: Double, slump s: Double) -> CGPath {
        let pts: [CGPoint] = (0..<ringPoints).map { i in
            let a = Double(i) / Double(ringPoints) * 2 * .pi
            let rad = 1 + (triangleRadius(a) - 1) * k
            return CGPoint(x: rad * cos(a) * (1 + 0.06 * s),
                           y: rad * sin(a) * (1 - 0.1 * s) - 0.1 * s)
        }
        return smoothClosedPath(pts)
    }

    /// Polar radius of an upward-pointing triangle whose corners are rounded by
    /// a smooth-min against a circle.
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

    /// Catmull-Rom through the ring → cubic Béziers.
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

    // MARK: - Eyes

    static func drawEyes(_ pose: BotPose, in ctx: CGContext, scale: Double) {
        // Tighter reach on the triangle, whose inscribed circle is smaller.
        let reach = 0.6 - 0.2 * pose.triangle
        var cx = pose.gazeX * reach
        var cy = pose.gazeY * reach - 0.12 * pose.triangle - 0.1 * pose.slump
        let limit = 0.52 - 0.15 * pose.triangle
        let d = hypot(cx, cy)
        if d > limit { cx *= limit / d; cy *= limit / d }
        // Foreshortening: eyes near the rim of the sphere narrow and huddle.
        let fx = (1 - 0.45 * cx * cx).squareRoot()
        let fy = (1 - 0.45 * cy * cy).squareRoot()

        let sep = (pose.eyes == .round ? 0.5 : 0.44) * fx * (1 + (scale - 1) * 0.5)
        for (side, lid) in [(-1.0, pose.lidLeft), (1.0, pose.lidRight)] {
            let rise = pose.eyes == .dash ? 0.04 * side : 0
            let c = CGPoint(x: cx + side * sep / 2, y: cy + rise)
            eye(pose.eyes, side: side, lid: lid, at: c, fx: fx * scale, fy: fy * scale, in: ctx)
        }
    }

    static func eye(_ kind: BotEyes, side: Double, lid: Double, at c: CGPoint,
                    fx: Double, fy: Double, in ctx: CGContext) {
        func lerp(_ a: Double, _ b: Double) -> Double { a + (b - a) * lid }
        let deg = Double.pi / 180
        switch kind {
        case .dash:
            capsule(at: c, w: lerp(0.14, 0.24) * fx, h: lerp(0.38, 0.06) * fy,
                    angle: lerp(27, -6) * deg, in: ctx)
        case .angry:
            capsule(at: c, w: lerp(0.13, 0.22) * fx, h: lerp(0.3, 0.06) * fy,
                    angle: lerp(55, 10) * deg * -side, in: ctx)
        case .round:
            ellipse(at: c, w: lerp(0.3, 0.34) * fx, h: lerp(0.44, 0.05) * fy, angle: 10 * deg, in: ctx)
        case .happy:
            let w = 0.3 * fx, h = lerp(0.16, 0.03) * fy
            let arc = CGMutablePath()
            arc.move(to: CGPoint(x: c.x - w / 2, y: c.y - h / 2))
            arc.addQuadCurve(to: CGPoint(x: c.x + w / 2, y: c.y - h / 2),
                             control: CGPoint(x: c.x, y: c.y + h * 1.5))
            ctx.addPath(arc)
            ctx.setLineWidth(0.11 * min(fx, fy))
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
