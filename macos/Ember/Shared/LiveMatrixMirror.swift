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
            .accessibilityValue(currentApp)
            .task { await env.live.track(.screen) }
    }

    /// The clock's current app, when a view is also holding clock health.
    private var currentApp: String {
        guard let app = env.live.clockHealth.value?.device?.currentApp, !app.isEmpty else {
            return env.live.screen.value == nil ? String(localized: "Not available") : ""
        }
        return AppNames.display(app)
    }
}
