import CoreGraphics

/// Where the clock's LED matrix sits inside the space a view is offered.
///
/// Cells are square and their pitch is a whole number of points, so every LED
/// lands on the same pixel pattern and the panel keeps its exact 4:1 shape at
/// any window size. A fractional pitch is what made resized previews smear:
/// neighbouring cells rounded to different widths and the glow bled across.
public struct LEDMatrixLayout: Equatable, Sendable {
    /// Smallest pitch drawn; below it the dots stop reading as a panel.
    public static let minPitch: CGFloat = 3
    /// Largest pitch; past it the panel is just big, not clearer.
    public static let maxPitch: CGFloat = 20
    /// Pitch when the container offers no size at all.
    public static let idealPitch: CGFloat = 10
    /// Below this pitch the per-cell glow is skipped: it would only muddy dots.
    public static let glowMinPitch: CGFloat = 6

    public let columns: Int
    public let rows: Int
    /// Points per cell, a whole number in `minPitch...maxPitch`.
    public let pitch: CGFloat
    /// The panel itself: `columns × pitch` by `rows × pitch`.
    public let size: CGSize
    /// Top-left of the panel, centred in the container and snapped to device
    /// pixels. Zero along an axis the container leaves open (nil or infinite).
    /// Negative when the container is smaller than the minimum panel.
    public let origin: CGPoint

    /// - Parameters:
    ///   - width, height: the offered space; nil or infinite means unconstrained.
    ///   - scale: device pixels per point, for snapping the origin.
    ///   - maxPitch: a caller's own cap (settings previews stay smaller).
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
            pitch = cap // offered unbounded space: take the largest panel
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

    /// Cell `(x, y)` relative to the panel's own top-left.
    public func cell(x: Int, y: Int) -> CGRect {
        CGRect(x: CGFloat(x) * pitch, y: CGFloat(y) * pitch, width: pitch, height: pitch)
    }

    /// The LED inside a cell: the same gap on every side, so dots stay crisp
    /// and evenly spaced.
    public func led(x: Int, y: Int) -> CGRect {
        cell(x: x, y: y).insetBy(dx: gap, dy: gap)
    }

    /// Gap between a cell's edge and its LED: about 1/7 of the pitch in whole
    /// points, half a point on the smallest panels so their LEDs aren't mere
    /// specks (a device pixel on Retina).
    public var gap: CGFloat { max(0.5, (pitch * 0.14).rounded()) }

    /// Corner radius of an LED, proportional to its size.
    public var cornerRadius: CGFloat { (pitch - 2 * gap) * 0.22 }

    /// Whether lit cells get a halo; tiny panels draw plain dots.
    public var showsGlow: Bool { pitch >= Self.glowMinPitch }
}
