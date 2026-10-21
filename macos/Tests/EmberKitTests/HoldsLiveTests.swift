import Testing
import Foundation
@testable import EmberKit

#if canImport(AppKit)
import SwiftUI
import AppKit

@MainActor @Observable private final class PageBox { var page = "bot" }

private struct PreviewStub: View {
    let box: PageBox
    let live: LiveModel
    var body: some View {
        Text(verbatim: box.page)
            .holdsLive(.weather, in: live, while: box.page == "weather")
    }
}

@MainActor private func settle(_ host: NSHostingView<PreviewStub>) async {
    host.layoutSubtreeIfNeeded()
    for _ in 0..<5 { await Task.yield(); try? await Task.sleep(for: .milliseconds(20)) }
}

@MainActor @Test func holdFollowsTheActivePage() async {
    let clock = ManualClock()
    let live = LiveModel(now: { Date() }, makeCoordinator: {
        RefreshCoordinator(fetch: $0, sleep: clock.sleepFn, now: clock.nowFn)
    })
    let box = PageBox()
    let host = NSHostingView(rootView: PreviewStub(box: box, live: live))
    host.frame = NSRect(x: 0, y: 0, width: 200, height: 40)
    let window = NSWindow(contentRect: host.frame, styleMask: [.titled], backing: .buffered, defer: true)
    window.contentView = host
    await settle(host)
    #expect(!live.isTracked(.weather))
    box.page = "weather"
    await settle(host)
    #expect(live.isTracked(.weather))
    box.page = "bot"
    await settle(host)
    #expect(!live.isTracked(.weather))
}
#endif
