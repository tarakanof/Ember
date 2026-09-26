import SwiftUI
import EmberKit

/// The clock's 32×8 matrix, live. Holds the `.screen` feed while on screen
/// (about one frame a second through the server, falling back to the clock
/// directly on servers that predate the proxy; pacing lives in the feed), so
/// every mirror on screen shares one poll. Black while nothing has loaded.
struct LiveMatrixMirror: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        MatrixScreenView(pixels: env.live.screen.value ?? Array(repeating: 0, count: 256))
            .accessibilityLabel("Clock display")
            .accessibilityValue(env.live.screen.value == nil ? String(localized: "Not available") : "")
            .task { await env.live.track(.screen) }
    }
}
