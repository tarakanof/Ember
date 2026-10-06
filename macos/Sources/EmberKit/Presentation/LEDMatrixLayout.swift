import CoreGraphics

public struct LEDMatrixLayout: Equatable, Sendable {
    public static let minPitch: CGFloat = 3
    public static let maxPitch: CGFloat = 20
    public static let idealPitch: CGFloat = 10
    public static let glowMinPitch: CGFloat = 6

    public let columns: Int
    public let rows: Int
    public let pitch: CGFloat
    public let size: CGSize
    public let origin: CGPoint

    public init(width: CGFloat?, height: CGFloat?, columns: Int = 32, rows: Int = 8,
                scale: CGFloat = 1, maxPitch: CGFloat = LEDMatrixLayout.maxPitch) {
        let cols = max(columns, 1), rws = max(rows, 1)
        let cap = max(Self.minPitch, maxPitch.rounded(.down))
        let fits = [width.map { $0 / CGFloat(cols) }, height.map { $0 / CGFloat(rws) }]
            .compactMap { $0 }
            .filter { $0.isFinite }
        let pitch: CGFloat
        if let tightest = fits.min() {
            pitch = min(max(tightest.rounded(.down), Self.minPitch), cap)
        } else if width == nil && height == nil {
            pitch = min(Self.idealPitch, cap)
        } else {
            pitch = cap
        }
        let size = CGSize(width: pitch * CGFloat(cols), height: pitch * CGFloat(rws))
        let s = scale > 0 ? scale : 1
        func centre(_ offered: CGFloat?, _ used: CGFloat) -> CGFloat {
            guard let offered, offered.isFinite else { return 0 }
            return ((offered - used) / 2 * s).rounded(.down) / s
        }
        self.columns = cols
        self.rows = rws
        self.pitch = pitch
        self.size = size
        self.origin = CGPoint(x: centre(width, size.width), y: centre(height, size.height))
    }

    public func cell(x: Int, y: Int) -> CGRect {
        CGRect(x: CGFloat(x) * pitch, y: CGFloat(y) * pitch, width: pitch, height: pitch)
    }

    public func led(x: Int, y: Int) -> CGRect {
        cell(x: x, y: y).insetBy(dx: gap, dy: gap)
    }

    public var gap: CGFloat { max(0.5, (pitch * 0.14).rounded()) }

    public var cornerRadius: CGFloat { (pitch - 2 * gap) * 0.22 }

    public var showsGlow: Bool { pitch >= Self.glowMinPitch }
}
