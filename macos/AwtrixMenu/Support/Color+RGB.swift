import SwiftUI
import AwtrixMenuKit

extension Color {
    init(_ rgb: RGB) {
        self.init(.sRGB,
                  red: Double(rgb.r) / 255.0,
                  green: Double(rgb.g) / 255.0,
                  blue: Double(rgb.b) / 255.0)
    }
}
