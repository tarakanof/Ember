#if DEBUG
import AppKit
import SwiftUI
import EmberKit

/// Knob section scenarios built on `KnobStatsFake`, for previews and the
/// snapshot render.
@MainActor
enum KnobFixtures {
    static func input(_ stats: Loadable<KnobStats>, range: KnobStatsRange = .fifteenMinutes) -> KnobDashboardInput {
        KnobDashboardInput(knobID: "knob-61fc8c", name: "Desk knob", firmware: "0.6.0", stats: stats, range: range)
    }

    static func loaded(_ range: KnobStatsRange, _ diagnostics: KnobDiagnostics = .full, now: Date,
                       online: Bool = true, gap: ClosedRange<TimeInterval>? = nil) -> KnobDashboardInput {
        let s = KnobStatsFake.make(range: range, diagnostics: diagnostics, now: now, online: online, gap: gap)
        return input(.loaded(s, at: now), range: range)
    }

    /// Name, scenario and colour scheme of every snapshot.
    static func scenarios(now: Date) -> [(name: String, input: KnobDashboardInput, scheme: ColorScheme)] {
        [
            ("knob-full-15m-light", loaded(.fifteenMinutes, now: now), .light),
            ("knob-full-15m-dark", loaded(.fifteenMinutes, now: now), .dark),
            ("knob-basic-1h-light", loaded(.hour, .basic, now: now, gap: 1500...2100), .light),
            ("knob-full-24h-dark-offline", loaded(.day, now: now, online: false, gap: 30_000...36_000), .dark),
            ("knob-diagnostics-off-light", loaded(.fifteenMinutes, .off, now: now), .light),
            ("knob-waiting-dark", input(.loaded(KnobStats(deviceID: "knob-61fc8c", diagnostics: .basic, range: .fifteenMinutes,
                                                          online: true, lastSeen: now, latest: nil, points: []), at: now)), .dark),
        ]
    }
}

/// Renders the knob section's scenarios to PNGs and quits: launch a Debug
/// build with `EMBER_KNOB_SNAPSHOTS=<dir>`. It draws through an off-screen
/// window so AppKit-backed controls (the segmented picker) render too,
/// which `ImageRenderer` leaves blank, and makes it key so the default
/// button draws prominent.
@MainActor
enum KnobSnapshotRenderer {
    nonisolated static var isRequested: Bool { ProcessInfo.processInfo.environment["EMBER_KNOB_SNAPSHOTS"] != nil }

    static func runIfRequested() {
        guard let dir = ProcessInfo.processInfo.environment["EMBER_KNOB_SNAPSHOTS"] else { return }
        let out = URL(fileURLWithPath: dir, isDirectory: true)
        try? FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)
        let now = Date()
        Task { @MainActor in
            for s in KnobFixtures.scenarios(now: now) {
                await render(s.input, scheme: s.scheme, now: now, to: out.appending(path: "\(s.name).png"))
            }
            exit(0)
        }
    }

    private static func render(_ input: KnobDashboardInput, scheme: ColorScheme, now: Date, to url: URL) async {
        let width: CGFloat = 1000
        let root = KnobDashboardSection(input: input, columns: 2, now: now)
            .padding(20)
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
        try? await Task.sleep(for: .milliseconds(600))
        host.layoutSubtreeIfNeeded()
        guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { return }
        host.cacheDisplay(in: host.bounds, to: rep)
        try? rep.representation(using: .png, properties: [:])?.write(to: url)
        window.orderOut(nil)
    }
}

#Preview("Knob — full, 15 min") {
    ScrollView { KnobDashboardSection(input: KnobFixtures.loaded(.fifteenMinutes, now: .now), columns: 2).padding(20) }
        .frame(width: 1000, height: 1400)
}
#Preview("Knob — basic, 1 h, gap") {
    ScrollView { KnobDashboardSection(input: KnobFixtures.loaded(.hour, .basic, now: .now, gap: 1500...2100), columns: 2).padding(20) }
        .frame(width: 1000, height: 1400)
}
#Preview("Knob — 24 h offline") {
    ScrollView { KnobDashboardSection(input: KnobFixtures.loaded(.day, now: .now, online: false), columns: 3).padding(20) }
        .frame(width: 1100, height: 1100)
}
#Preview("Knob — diagnostics off") {
    KnobDashboardSection(input: KnobFixtures.loaded(.fifteenMinutes, .off, now: .now)).padding(20).frame(width: 900)
}
#Preview("Knob — loading") {
    ScrollView { KnobDashboardSection(input: KnobFixtures.input(.loading), columns: 2).padding(20) }
        .frame(width: 1000, height: 1400)
}
#Preview("Knob — server too old") {
    KnobDashboardSection(input: KnobFixtures.input(.failed(.featureOff, last: nil, lastAt: nil))).padding(20).frame(width: 900)
}
#endif
