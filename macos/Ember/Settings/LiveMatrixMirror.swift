import SwiftUI
import EmberKit

/// Self-contained live mirror of the clock's 32×8 matrix, reused by the Device
/// and Agent tabs. Polls the server's `/v1/device/screen` proxy while
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
        // At 1 Hz this loop is the single heaviest caller of the server, so it is
        // also the one most likely to be throttled — and, if it just carried on at
        // cadence, to keep everything else throttled too. Which source to ask and
        // how long to wait live in MirrorPoller, where they're unit-tested.
        var poller = MirrorPoller()
        while !Task.isCancelled {
            var s: [Int]?
            if poller.probesProxy {
                do {
                    s = try await env.device.screen()
                    poller.record(.pixels)
                } catch let e as APIError where e.isRateLimited {
                    poller.record(.throttled(e.retryAfter ?? RateLimitBackoff.fallbackRetryAfter))
                } catch {
                    poller.record(.failed)
                }
            }
            if poller.triesDirect(havePixels: s != nil), let base = clockBaseURL, !base.isEmpty {
                s = try? await DeviceService.directScreen(clockBaseURL: base)
            }
            if Task.isCancelled { return }
            screen = s
            try? await Task.sleep(for: poller.endTick(havePixels: s != nil))
        }
    }
}
