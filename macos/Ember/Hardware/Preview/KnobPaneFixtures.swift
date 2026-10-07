#if DEBUG
import AppKit
import SwiftUI
import EmberKit

@MainActor
enum KnobPaneFixtures {
    static let pages: [(name: String, page: DevicePage, height: CGFloat)] = [
        ("knob-pane-status", .hardware(.status), 1500),
        ("knob-pane-display", .hardware(.display), 420),
        ("knob-pane-behavior", .hardware(.behavior), 760),
    ]

    static func environment(now: Date) async -> AppEnvironment {
        let env = AppEnvironment(producerEnvPath: FileManager.default.temporaryDirectory
            .appending(path: "ember-knob-fixture-\(UUID().uuidString).env"))
        KnobFixtureURLProtocol.responses = responses(now: now)
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [KnobFixtureURLProtocol.self]
        let client = APIClient(baseURL: URL(string: "http://\(KnobFixtureURLProtocol.host)"), token: "fixture",
                               session: URLSession(configuration: config), pathStatus: { .satisfied })
        env.knob.configure(service: KnobService(client: client))
        await env.knob.load()
        return env
    }

    static func responses(now: Date) -> [String: String] {
        let iso = { (d: Date) in d.formatted(.iso8601) }
        let checkin = #"""
        {"seen_at":"\#(iso(now.addingTimeInterval(-20)))","fw":"0.9.15","fw_build":"a1b2c3d4","ip":"192.0.2.61","rssi":-67,
         "heap_internal_free":47104,"heap_internal_largest":31744,"uptime_s":93784,"applied_version":7,"link_mhz":80,
         "wifi":{"bssid":"78:45:58:4b:c2:cd","channel":6,"disconnects":3,"last_reason":203,"rssi_min":-79},
         "diag":{"boots":42,"reboots":3,"reset_reason":"panic","prev_reset_reason":"poweron","heap_internal_min":38912,"heap_largest_min":30720,
                 "crash":{"id":"1a2b3c4d","size":65536,"pc":"0x4201a2b3","reason":"panic","task":"ember","elf":"a1b2c3d4"}}}
        """#
        let device = #"{"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"3cdc7561fc8c","name":"Desk knob","created_at":"2026-10-04T10:00:00Z","config_version":7,"rotation_pending":false,"rotated_at":null,"last_checkin":\#(checkin)}"#
        let dumps = #"""
        [{"id":"1a2b3c4d","size":65536,"fw":"0.9.15","received_at":"\#(iso(now.addingTimeInterval(-3_600)))","reason":"panic","task":"ember","pc":"0x4201a2b3","elf":"a1b2c3d4"},
         {"id":"0badc0de","size":65536,"fw":"0.9.14","received_at":"\#(iso(now.addingTimeInterval(-86_400)))","reason":"unknown","task":"prof","pc":"0xfffffffe","elf":"c0ffee13"},
         {"id":"5eed5eed","size":4096,"fw":"0.9.14","received_at":"\#(iso(now.addingTimeInterval(-172_800)))","reason":"int_wdt","task":"IDLE0","pc":"0x40380f1c","elf":"feedface"}]
        """#
        let config = #"""
        {"home":"bot","poll_ms":2000,"diagnostics":"full","stats_interval_s":60,"live_interval_s":5,"display":{"fast_link":true},
         "brightness":{"follow_ember":false,"level":153,"floor":10,"startup":156},
         "pages":[{"id":"bot","on":true},{"id":"pomodoro","on":true},{"id":"weather","on":true},{"id":"nowplaying","on":false}],
         "bot":{"sleepy_after_s":600,"demo_hold_s":15,"source_label":true,"working_ring":true}}
        """#
        let ota = #"{"mode":"manual","phase":"idle","available":"0.9.16","running":{"fw":"0.9.15","build":"a1b2c3d4","rollback":true}}"#
        let image = { (v: String, build: String, channel: String, ago: TimeInterval) in
            #"{"version":"\#(v)","build":"\#(build)","channel":"\#(channel)","elf":true,"idf_ver":"v5.5.1","project":"cinder","sha256":"00","size":1677721,"uploaded_at":"\#(iso(now.addingTimeInterval(-ago)))"}"#
        }
        let firmware = "[\(image("0.9.16", "b2c3d4e5", "release", 7_200)),\(image("0.9.15", "a1b2c3d4", "release", 172_800)),\(image("0.9.13", "c0ffee13", "test", 604_800))]"
        return [
            "/v1/devices": #"{"devices":[\#(device)]}"#,
            "/v1/devices/knob-61fc8c/config": config,
            "/v1/devices/knob-61fc8c/coredumps": dumps,
            "/v1/devices/knob-61fc8c/ota": ota,
            "/v1/firmware": firmware,
        ]
    }

    static func render(to out: URL, now: Date) async {
        let env = await environment(now: now)
        for page in pages {
            for scheme in [ColorScheme.light, .dark] {
                let name = "\(page.name)-\(scheme == .dark ? "dark" : "light").png"
                await render(KnobDetail(page: page.page).environment(env), height: page.height, scheme: scheme,
                             to: out.appending(path: name))
            }
        }
    }

    private static func render(_ view: some View, height: CGFloat, scheme: ColorScheme, to url: URL) async {
        let width: CGFloat = 580
        let root = view
            .frame(width: width, height: height)
            .background(Color(nsColor: .windowBackgroundColor))
            .environment(\.colorScheme, scheme)
        let host = NSHostingView(rootView: root)
        let window = NSWindow(contentRect: NSRect(x: -10_000, y: -10_000, width: width, height: height),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.appearance = NSAppearance(named: scheme == .dark ? .darkAqua : .aqua)
        window.contentView = host
        host.frame.size = NSSize(width: width, height: height)
        NSApp.setActivationPolicy(.regular)
        NSApp.activate()
        window.makeKeyAndOrderFront(nil)
        try? await Task.sleep(for: .milliseconds(1200))
        host.layoutSubtreeIfNeeded()
        guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { return }
        host.cacheDisplay(in: host.bounds, to: rep)
        try? rep.representation(using: .png, properties: [:])?.write(to: url)
        window.orderOut(nil)
    }
}

final class KnobFixtureURLProtocol: URLProtocol {
    static let host = "knob-fixture.invalid"
    nonisolated(unsafe) static var responses: [String: String] = [:]

    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == host }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        guard let url = request.url else { return }
        let body = request.httpMethod == "GET" ? Self.responses[url.path] : nil
        let status = request.httpMethod == "GET" ? (body == nil ? 404 : 200) : 204
        let resp = HTTPURLResponse(url: url, statusCode: status, httpVersion: nil,
                                   headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data((body ?? #"{"error":"not found"}"#).utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
#endif
