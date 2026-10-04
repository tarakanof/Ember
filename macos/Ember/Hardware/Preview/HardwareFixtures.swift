#if DEBUG
import AppKit
import SwiftUI
import EmberKit

/// Hardware page scenarios built on `KnobStatsFake` and `ClockStatsFake`,
/// for previews and the snapshot render.
@MainActor
enum HardwareFixtures {
    static func knob(_ stats: Loadable<KnobStats>, range: HardwareRange = .fifteenMinutes) -> KnobHardwareInput {
        KnobHardwareInput(knobID: "knob-61fc8c", firmware: "0.6.0", ipAddress: "192.0.2.61", stats: stats, range: range)
    }

    static func knob(_ range: HardwareRange, _ diagnostics: KnobDiagnostics = .full, now: Date,
                     online: Bool = true, gap: ClosedRange<TimeInterval>? = nil) -> KnobHardwareInput {
        let s = KnobStatsFake.make(range: range, diagnostics: diagnostics, now: now, online: online, gap: gap)
        return knob(.loaded(s, at: now), range: range)
    }

    static func clock(_ stats: Loadable<ClockStats>, range: HardwareRange = .hour, now: Date) -> ClockHardwareInput {
        ClockHardwareInput(stats: stats, health: health(now: now), range: range)
    }

    static func clock(_ range: HardwareRange, now: Date, online: Bool = true,
                      gap: ClosedRange<TimeInterval>? = nil) -> ClockHardwareInput {
        clock(.loaded(ClockStatsFake.make(range: range, now: now, online: online, gap: gap), at: now), range: range, now: now)
    }

    /// `/v1/clock/health` for the facts: an update waiting, 99 % delivered.
    static func health(now: Date) -> ClockHealth {
        let iso = { (d: Date) in d.formatted(.iso8601) }
        let json = #"""
        {"generated_at":"\#(iso(now))","publish":{"counting_since":"\#(iso(now.addingTimeInterval(-90_000)))","ok_24h":2871,"fail_24h":12,"success_ratio_24h":0.9958,"ok_total":2990,"fail_total":12,"retries_total":3,"last_at":"\#(iso(now))","last_ok":true},
         "device":{"reachable":true,"checked_at":"\#(iso(now))","firmware":"1.1.1","current_app":"Time","uptime_sec":268719,"free_heap_bytes":103032,"min_free_heap_bytes":76544,"wifi_rssi_dbm":-66,"wifi_connects":1,"reset_reason":"software","fps":42,"matrix_power":true,"battery_percent":96,"low_battery":false,"temperature_c":31.5,"humidity_percent":38},
         "latest_firmware":"1.1.2","update_available":true}
        """#
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return try! d.decode(ClockHealth.self, from: Data(json.utf8))
    }

    enum Scenario {
        case knob(KnobHardwareInput)
        case clock(ClockHardwareInput)
    }

    /// Name, scenario and colour scheme of every snapshot.
    static func scenarios(now: Date) -> [(name: String, scenario: Scenario, scheme: ColorScheme)] {
        [
            ("clock-1h-light", .clock(clock(.hour, now: now)), .light),
            ("clock-1h-dark", .clock(clock(.hour, now: now)), .dark),
            ("clock-15m-light", .clock(clock(.fifteenMinutes, now: now)), .light),
            ("clock-24h-dark-offline", .clock(clock(.day, now: now, online: false, gap: 30_000...36_000)), .dark),
            ("clock-waiting-light", .clock(clock(.loaded(ClockStats(range: .hour, reachable: nil, checkedAt: nil,
                                                                    latest: nil, points: []), at: now), now: now)), .light),
            ("knob-full-15m-light", .knob(knob(.fifteenMinutes, now: now)), .light),
            ("knob-full-15m-dark", .knob(knob(.fifteenMinutes, now: now)), .dark),
            ("knob-basic-1h-light", .knob(knob(.hour, .basic, now: now, gap: 1500...2100)), .light),
            ("knob-full-24h-dark-offline", .knob(knob(.day, now: now, online: false, gap: 30_000...36_000)), .dark),
            ("knob-diagnostics-off-light", .knob(knob(.fifteenMinutes, .off, now: now)), .light),
        ]
    }
}

/// The content of a Hardware page as Settings lays it out, for previews and
/// snapshots.
struct HardwareFixtureView: View {
    let scenario: HardwareFixtures.Scenario
    var columns = 2
    var now = Date()

    var body: some View {
        VStack(alignment: .leading, spacing: HardwareMetrics.spacing) {
            switch scenario {
            case .knob(let input): KnobHardwareContent(input: input, columns: columns, now: now)
            case .clock(let input): ClockHardwareContent(input: input, columns: columns, now: now)
            }
        }
        .padding(20)
    }
}

/// Renders the Hardware pages' scenarios to PNGs and quits: launch a Debug
/// build with `EMBER_HARDWARE_SNAPSHOTS=<dir>`. It draws through an
/// off-screen window so AppKit-backed controls (the segmented picker)
/// render too, which `ImageRenderer` leaves blank, and makes it key so the
/// default button draws prominent.
@MainActor
enum HardwareSnapshotRenderer {
    nonisolated static let environmentKey = "EMBER_HARDWARE_SNAPSHOTS"

    nonisolated static var isRequested: Bool { ProcessInfo.processInfo.environment[environmentKey] != nil }

    static func runIfRequested() {
        guard let dir = ProcessInfo.processInfo.environment[environmentKey] else { return }
        let out = URL(fileURLWithPath: dir, isDirectory: true)
        try? FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)
        let now = Date()
        Task { @MainActor in
            for s in HardwareFixtures.scenarios(now: now) {
                await render(s.scenario, scheme: s.scheme, now: now, to: out.appending(path: "\(s.name).png"))
            }
            exit(0)
        }
    }

    /// The Settings window's detail column width.
    static let width: CGFloat = 580

    private static func render(_ scenario: HardwareFixtures.Scenario, scheme: ColorScheme, now: Date, to url: URL) async {
        let root = HardwareFixtureView(scenario: scenario, columns: HardwareMetrics.columns(forWidth: width - 40), now: now)
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

#Preview("Clock — 1 h") {
    ScrollView { HardwareFixtureView(scenario: .clock(HardwareFixtures.clock(.hour, now: .now))) }
        .frame(width: 580, height: 1400)
}
#Preview("Clock — 24 h offline") {
    ScrollView { HardwareFixtureView(scenario: .clock(HardwareFixtures.clock(.day, now: .now, online: false))) }
        .frame(width: 580, height: 1400)
}
#Preview("Clock — loading") {
    ScrollView { HardwareFixtureView(scenario: .clock(HardwareFixtures.clock(.loading, now: .now))) }
        .frame(width: 580, height: 1400)
}
#Preview("Knob — full, 15 min") {
    ScrollView { HardwareFixtureView(scenario: .knob(HardwareFixtures.knob(.fifteenMinutes, now: .now))) }
        .frame(width: 580, height: 1600)
}
#Preview("Knob — basic, 1 h, gap") {
    ScrollView { HardwareFixtureView(scenario: .knob(HardwareFixtures.knob(.hour, .basic, now: .now, gap: 1500...2100))) }
        .frame(width: 580, height: 1400)
}
#Preview("Knob — diagnostics off") {
    HardwareFixtureView(scenario: .knob(HardwareFixtures.knob(.fifteenMinutes, .off, now: .now))).frame(width: 580)
}
#Preview("Knob — server too old") {
    HardwareFixtureView(scenario: .knob(HardwareFixtures.knob(.failed(.featureOff, last: nil, lastAt: nil)))).frame(width: 580)
}
#endif
