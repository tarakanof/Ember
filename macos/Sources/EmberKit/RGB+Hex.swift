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
