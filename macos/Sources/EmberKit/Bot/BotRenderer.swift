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
    /// Extra eye width/height in body radii, so small sizes can add whole pixels.
    public var eyeGrow: CGSize = .zero
    /// App-icon backing plate (macOS 26+ grid); nil draws the bare ball.
    public var plate: CGColor?

    public init(body: CGColor, eyes: CGColor?, rim: CGColor? = nil, badge: CGColor? = nil,
                shadow: Bool = false, fill: Double, eyeScale: Double = 1, hopScale: Double = 1,
                plate: CGColor? = nil) {
        self.body = body; self.eyes = eyes; self.rim = rim; self.badge = badge
        self.shadow = shadow; self.fill = fill; self.eyeScale = eyeScale; self.hopScale = hopScale
        self.plate = plate
    }

    /// For a 22 pt image (the HIG's max menu-bar asset height): a 16 pt ball,
    /// the size at which a circular extra matches the system icons' weight.
    public static func menuBar(tint: CGColor) -> BotStyle {
        var s = BotStyle(body: tint, eyes: nil, fill: 16.0 / 22, eyeScale: 1.25, hopScale: 0.5)
        s.eyeGrow = CGSize(width: 0.5 / 8, height: 0.5 / 8)   // +1 Retina px on the 8 pt radius
        return s
    }

    /// App-icon grid: an 824/1024 rounded plate, the ball filling most of it.
    public static func dock(badge: CGColor?) -> BotStyle {
        BotStyle(body: CGColor(srgbRed: 0.04, green: 0.04, blue: 0.045, alpha: 1),
                 eyes: CGColor(gray: 1, alpha: 1),
                 badge: badge, shadow: true, fill: 0.62, hopScale: 0.6,
                 plate: CGColor(srgbRed: 0.98, green: 0.98, blue: 0.98, alpha: 1))
    }

    /// Apple's macOS 26 icon template: 824 px plate on a 1024 px canvas.
    static let plateSize = 824.0 / 1024
    static let plateCorner = 0.225
}

/// Draws the bot in code — a 96-point body ring (so shapes morph point by point)
/// plus capsule/oval/arc eyes. Coordinates are y-up.
public enum BotRenderer {
    public static func draw(_ pose: BotPose, in ctx: CGContext, rect: CGRect, style: BotStyle) {
        let half = min(rect.width, rect.height) / 2
        let r = half * style.fill
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
            // Keep hops and squash inside the plate, like the reference tile.
            ctx.addPath(shape)
            ctx.clip()
        }

        if style.shadow {
            ctx.setShadow(offset: CGSize(width: 0, height: -0.04 * r), blur: 0.1 * r,
                          color: CGColor(gray: 0, alpha: 0.22))
        }
        // Bare ball: sit low to leave hop headroom. On a plate: dead centre.
        ctx.translateBy(x: rect.midX, y: rect.midY - (style.plate == nil ? 0.12 * r : 0))
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
        ctx.saveGState()
        ctx.addPath(body)
        ctx.clip()                                 // eyes never spill off the triangle
        drawEyes(pose, in: ctx, scale: style.eyeScale, grow: style.eyeGrow)
        ctx.restoreGState()
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

    static func drawEyes(_ pose: BotPose, in ctx: CGContext, scale: Double, grow: CGSize = .zero) {
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
            eye(pose.eyes, side: side, lid: lid, at: c, fx: fx * scale, fy: fy * scale, grow: grow, in: ctx)
        }
    }

    static func eye(_ kind: BotEyes, side: Double, lid: Double, at c: CGPoint,
                    fx: Double, fy: Double, grow: CGSize = .zero, in ctx: CGContext) {
        let gw = grow.width, gh = grow.height * (1 - lid)   // shut lids stay thin
        func lerp(_ a: Double, _ b: Double) -> Double { a + (b - a) * lid }
        let deg = Double.pi / 180
        switch kind {
        case .dash:
            capsule(at: c, w: lerp(0.14, 0.24) * fx + gw, h: lerp(0.38, 0.06) * fy + gh,
                    angle: lerp(27, -6) * deg, in: ctx)
        case .angry:
            capsule(at: c, w: lerp(0.13, 0.22) * fx + gw, h: lerp(0.3, 0.06) * fy + gh,
                    angle: lerp(55, 10) * deg * -side, in: ctx)
        case .round:
            ellipse(at: c, w: lerp(0.3, 0.34) * fx + gw, h: lerp(0.44, 0.05) * fy + gh, angle: 10 * deg, in: ctx)
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
