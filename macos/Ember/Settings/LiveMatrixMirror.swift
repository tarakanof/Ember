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
        var preferProxy = true
        var tick = 0
        // At 1 Hz this loop is the single heaviest caller of the server, so it is
        // also the one most likely to be throttled — and to keep everything else
        // throttled if it just carries on at cadence.
        var pacer = RateLimitBackoff(base: .seconds(1))
        while !Task.isCancelled {
            var s: [Int]?
            var throttled: Duration?
            if preferProxy || tick % 30 == 0 {
                do {
                    s = try await env.device.screen()
                } catch let e as APIError where e.isRateLimited {
                    throttled = e.retryAfter ?? RateLimitBackoff.fallbackRetryAfter
                } catch {
                    s = nil
                }
                // A throttled proxy call says nothing about whether the proxy
                // works, so don't fall back to talking to the clock directly.
                if throttled == nil { preferProxy = s != nil }
            }
            if s == nil, throttled == nil, let base = clockBaseURL, !base.isEmpty {
                s = try? await DeviceService.directScreen(clockBaseURL: base)
            }
            if Task.isCancelled { return }
            if throttled == nil { screen = s }
            tick += 1
            let delay = throttled.map { pacer.nextDelay(after: .rateLimited(retryAfter: $0)) }
                ?? (s == nil ? .seconds(3) : pacer.nextDelay(after: .succeeded))
            try? await Task.sleep(for: delay)
        }
    }
}
