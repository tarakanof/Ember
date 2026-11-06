#if DEBUG
import AppKit
import SwiftUI
import EmberKit

@MainActor
enum DashboardSnapshotRenderer {
    nonisolated static let environmentKey = "EMBER_DASHBOARD_SNAPSHOTS"

    nonisolated static var isRequested: Bool { ProcessInfo.processInfo.environment[environmentKey] != nil }

    static let width: CGFloat = 1000

    static func runIfRequested() {
        guard let dir = ProcessInfo.processInfo.environment[environmentKey] else { return }
        let out = URL(fileURLWithPath: dir, isDirectory: true)
        try? FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)
        Task { @MainActor in
            for scheme in [ColorScheme.light, .dark] {
                let name = scheme == .dark ? "dark" : "light"
                await render(dayCards(DashboardFixtures.established), scheme: scheme, to: out.appending(path: "day-\(name).png"))
            }
            await render(DashboardContent(source: DashboardFixtures.established), scheme: .light,
                         to: out.appending(path: "dashboard-light.png"))
            await render(dayCards(DashboardFixtures.newUser), scheme: .light, to: out.appending(path: "day-new-user.png"))
            await render(dayCards(DashboardFixtures.dayOff), scheme: .light, to: out.appending(path: "day-off.png"))
            exit(0)
        }
    }

    private static func dayCards(_ source: DashboardData) -> some View {
        VStack(spacing: DashboardContent<DashboardData>.spacing) {
            if source.showsDay {
                HStack(alignment: .top, spacing: DashboardContent<DashboardData>.spacing) {
                    DashboardCardSlot(id: .dayToday, source: source)
                    if source.showsDayHistory { DashboardCardSlot(id: .dayWeek, source: source) }
                }
                if source.showsDayHistory {
                    DashboardCardSlot(id: .dayStrip, source: source)
                    DashboardCardSlot(id: .stopTrend, source: source)
                    DashboardCardSlot(id: .dayWeeks, source: source)
                }
            }
        }
        .padding(20)
    }

    private static func render<V: View>(_ view: V, scheme: ColorScheme, to url: URL) async {
        let root = view
            .frame(width: width)
            .background(Color(nsColor: .windowBackgroundColor))
            .environment(\.colorScheme, scheme)
        let host = NSHostingView(rootView: root)
        let window = NSWindow(contentRect: NSRect(x: -10_000, y: -10_000, width: width, height: 800),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.appearance = NSAppearance(named: scheme == .dark ? .darkAqua : .aqua)
        window.contentView = host
        host.frame.size = host.fittingSize
        window.setContentSize(host.fittingSize)
        NSApp.setActivationPolicy(.regular)
        NSApp.activate()
        window.makeKeyAndOrderFront(nil)
        try? await Task.sleep(for: .milliseconds(800))
        host.layoutSubtreeIfNeeded()
        guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { return }
        host.cacheDisplay(in: host.bounds, to: rep)
        try? rep.representation(using: .png, properties: [:])?.write(to: url)
        window.orderOut(nil)
    }
}
#endif
