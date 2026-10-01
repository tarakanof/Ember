import AppKit
import SwiftUI
import EmberKit

extension Color {
    init(_ rgb: RGB) {
        self.init(.sRGB,
                  red: Double(rgb.r) / 255.0,
                  green: Double(rgb.g) / 255.0,
                  blue: Double(rgb.b) / 255.0)
    }
}

extension RGB {
    init(_ color: Color) {
        let ns = NSColor(color).usingColorSpace(.sRGB) ?? NSColor.black
        self.init(sRGB: Double(ns.redComponent),
                  g: Double(ns.greenComponent),
                  b: Double(ns.blueComponent))
    }
}
