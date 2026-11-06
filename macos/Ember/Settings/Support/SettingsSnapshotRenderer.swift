#if DEBUG
import AppKit
import SwiftUI
import EmberKit

@MainActor
enum SettingsSnapshotRenderer {
    nonisolated static let environmentKey = "EMBER_SETTINGS_SNAPSHOTS"

    nonisolated static var isRequested: Bool { ProcessInfo.processInfo.environment[environmentKey] != nil }

    static func runIfRequested() {
        guard let dir = ProcessInfo.processInfo.environment[environmentKey] else { return }
        let out = URL(fileURLWithPath: dir, isDirectory: true)
        try? FileManager.default.createDirectory(at: out, withIntermediateDirectories: true)
        Task { @MainActor in
            for scheme in [ColorScheme.light, .dark] {
                await render(SidebarSample(), scheme: scheme,
                             to: out.appending(path: "printer-sidebar-\(scheme == .dark ? "dark" : "light").png"))
            }
            exit(0)
        }
    }

    private struct SidebarSample: View {
        var body: some View {
            HStack(alignment: .top, spacing: 24) {
                List {
                    Section("Sources") {
                        row(String(localized: SourceID.weather.title), SourceID.weather.systemImage)
                        row(String(localized: SourceID.printer.title), SourceID.printer.systemImage)
                        row(String(localized: SourceID.music.title), SourceID.music.systemImage)
                    }
                    Section("Clock") {
                        row(String(localized: AppID.weather.title), AppID.weather.systemImage)
                        row(String(localized: AppID.print.title), AppID.print.systemImage)
                    }
                    Section("Knob") {
                        row(String(localized: AppID.weather.title), AppID.weather.systemImage)
                        row(String(localized: AppID.print.title), AppID.print.systemImage)
                    }
                }
                .listStyle(.sidebar)
                .frame(width: 240, height: 360)
                Form { SourceLinkSection(source: .printer) }
                    .formStyle(.grouped)
                    .frame(width: 380, height: 160)
                VStack(alignment: .leading, spacing: 18) {
                    ForEach([12.0, 16.0, 24.0, 48.0], id: \.self) { size in
                        HStack(spacing: 14) {
                            SymbolImage(EmberSymbol.printer3D).font(.system(size: size))
                            Image(systemName: "cloud.sun").font(.system(size: size))
                            Text(verbatim: "\(Int(size)) pt").foregroundStyle(.secondary)
                        }
                    }
                }
                .padding(.top, 20)
            }
            .padding(20)
        }

        private func row(_ title: String, _ symbol: String) -> some View {
            Label { Text(verbatim: title) } icon: { SymbolImage(symbol) }
        }
    }

    private static func render<V: View>(_ view: V, scheme: ColorScheme, to url: URL) async {
        let root = view
            .background(Color(nsColor: .windowBackgroundColor))
            .environment(\.colorScheme, scheme)
            .environment(\.controlActiveState, .key)
        let host = NSHostingView(rootView: root)
        let window = NSWindow(contentRect: NSRect(x: -10_000, y: -10_000, width: 900, height: 420),
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
