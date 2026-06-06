import Foundation

extension RGB {
    /// Parses a "#rrggbb" string (exactly 6 hex digits after #). Returns nil on
    /// any other shape. Used to draw the /v1/preview color grid in a SwiftUI Canvas.
    public init?(hex: String) {
        guard hex.count == 7, hex.hasPrefix("#") else { return nil }
        let body = hex.dropFirst()
        guard let value = UInt32(body, radix: 16), body.allSatisfy(\.isHexDigit) else { return nil }
        self.init(r: UInt8((value >> 16) & 0xff),
                  g: UInt8((value >> 8) & 0xff),
                  b: UInt8(value & 0xff))
    }
}

extension RGB {
    /// "#RRGGBB" (uppercase) — the producer.env / server wire format.
    public var hex: String { String(format: "#%02X%02X%02X", Int(r), Int(g), Int(b)) }

    /// Quantizes sRGB component doubles (each clamped to 0…1) to 8-bit RGB.
    public init(sRGB r: Double, g: Double, b: Double) {
        func q(_ v: Double) -> UInt8 { UInt8((min(1, max(0, v)) * 255).rounded()) }
        self.init(r: q(r), g: q(g), b: q(b))
    }
}
