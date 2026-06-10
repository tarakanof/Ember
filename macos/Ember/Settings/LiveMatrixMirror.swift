import SwiftUI
import EmberKit

/// Self-contained live mirror of the clock's 32×8 matrix, reused by the Device
/// and Code-agent tabs. Polls the server's `/v1/device/screen` proxy while
/// visible; against an older server that predates it (404), falls back to
/// reading the clock's `/api/screen` directly (what the AWTRIX app does). Backs
/// off to 3s while nothing is reachable. Renders through `MatrixScreenView`.
struct LiveMatrixMirror: View {
    @Environment(AppEnvironment.self) private var env

    @State private var screen: [Int]?
    @State private var clockBaseURL: String?

    var body: some View {
        MatrixScreenView(pixels: screen ?? Array(repeating: 0, count: 256))
            .task { await poll() }
    }

    private func poll() async {
        // The clock's address (for the direct fallback) is server-held config.
        clockBaseURL = (try? await env.device.config())?.baseURL
        var preferProxy = true
        var tick = 0
        while !Task.isCancelled {
            var s: [Int]?
            if preferProxy || tick % 30 == 0 {
                s = try? await env.device.screen()
                preferProxy = s != nil
            }
            if s == nil, let base = clockBaseURL, !base.isEmpty {
                s = try? await DeviceService.directScreen(clockBaseURL: base)
            }
            if Task.isCancelled { return }
            screen = s
            tick += 1
            try? await Task.sleep(for: .seconds(s == nil ? 3 : 1))
        }
    }
}
