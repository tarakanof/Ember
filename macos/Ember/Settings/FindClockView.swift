import SwiftUI
import EmberKit

/// Finds the AWTRIX clock and points the Ember server at it — a group of Form
/// rows (button, results, caption) meant to sit inside a `Section`.
///
/// One button, two scans: the server's own mDNS browse (`GET /v1/device/discover`)
/// runs first so the existing path keeps working, and this Mac's browse takes over
/// when the server comes up empty — the usual symptom of a container that can't
/// see multicast. The caption always names which one produced the list. Picking a
/// clock PUTs `/v1/device/config`, so the server stays the only writer to the
/// device either way.
struct FindClockView: View {
    /// The clock URL the server currently uses, to tick the matching row.
    var currentBaseURL: String?
    /// Runs after the server has been pointed at a different clock.
    var onPicked: () async -> Void = {}

    @Environment(AppEnvironment.self) private var env
    @State private var local = ClockDiscovery()
    @State private var serverFound: [DiscoveredClock] = []
    @State private var origin: Origin = .idle
    @State private var searching = false
    @State private var error: String?

    /// Which scan produced the rows on screen.
    private enum Origin { case idle, server, local }

    /// How long the local browse is given before "Searching…" gives up. The
    /// results list stays live afterwards — a late arrival still shows up.
    private let localScanWindow: Duration = .seconds(4)

    private var results: [DiscoveredClock] {
        origin == .local ? local.clocks : serverFound
    }

    var body: some View {
        Group {
            Button {
                Task { await find() }
            } label: {
                RowLabel(searching ? "Searching…" : "Find clock",
                         symbol: "antenna.radiowaves.left.and.right", tint: .teal)
            }
            .disabled(searching)
            // On the button, not the Group: a Group applies the modifier to every
            // child, so a result row being re-ordered mid-scan would stop the browse.
            .onDisappear { local.stop() }

            ForEach(results) { clock in
                Button {
                    Task { await pick(clock) }
                } label: {
                    row(clock)
                }
                .buttonStyle(.plain)
            }

            caption
        }
    }

    private func row(_ clock: DiscoveredClock) -> some View {
        let selected = clock.baseURL == currentBaseURL
        return HStack {
            Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(selected ? Color.green : Color.secondary)
            VStack(alignment: .leading) {
                Text(clock.baseURL)
                Text(clock.version.isEmpty ? clock.host : "\(clock.host) · fw \(clock.version)")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    @ViewBuilder private var caption: some View {
        if let error {
            Label(error, systemImage: "exclamationmark.triangle")
                .font(.caption).foregroundStyle(.red)
        }
        switch origin {
        case .idle:
            Text("Scans from the Ember server first, then from this Mac if the server's network can't see the clock.")
                .font(.caption).foregroundStyle(.secondary)
        case .server:
            Text("Found by the Ember server. Your choice is saved on the server and overrides its auto-discovery.")
                .font(.caption).foregroundStyle(.secondary)
        case .local:
            if !local.clocks.isEmpty {
                Text("Found by this Mac — the server's own scan came up empty. Your choice is saved on the server.")
                    .font(.caption).foregroundStyle(.secondary)
            } else if searching {
                Text("The server found nothing; scanning from this Mac…")
                    .font(.caption).foregroundStyle(.secondary)
            } else {
                emptyLocalScan
            }
        }
    }

    /// Both scans came up empty — say why, and offer the Local Network grant,
    /// which is the usual cause on a fresh install.
    @ViewBuilder private var emptyLocalScan: some View {
        switch local.status {
        case .needsAccess:
            Label("Local Network access needed", systemImage: "exclamationmark.triangle.fill")
                .font(.caption).foregroundStyle(.orange)
        case .unavailable:
            Label("Network discovery unavailable", systemImage: "wifi.exclamationmark")
                .font(.caption).foregroundStyle(.secondary)
        case .searching:
            Text("No clock found. Check that it's powered on and on this network.")
                .font(.caption).foregroundStyle(.secondary)
        }
        Button {
            openLocalNetworkSettings()
        } label: {
            Label("Grant Local Network Access…", systemImage: "lock.shield")
        }
    }

    /// Server scan first (it keeps working where it always did); this Mac's
    /// browse only when the server returns nothing or can't be reached.
    private func find() async {
        searching = true
        defer { searching = false }
        error = nil
        local.stop()
        origin = .idle
        serverFound = (try? await env.device.discover())?.candidates ?? []
        if !serverFound.isEmpty {
            origin = .server
            return
        }
        origin = .local
        local.restart()
        try? await Task.sleep(for: localScanWindow)
    }

    /// Persists the chosen clock on the server and lets the host tab reload.
    private func pick(_ clock: DiscoveredClock) async {
        do {
            try await env.device.setConfig(baseURL: clock.baseURL)
            local.stop()
            serverFound = []
            origin = .idle
            await onPicked()
        } catch {
            self.error = "Couldn't switch clock: \(error.localizedDescription)"
        }
    }
}
