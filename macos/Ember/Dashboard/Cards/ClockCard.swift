import SwiftUI
import EmberKit

/// Card 1: the clock's real LED matrix, what it's showing, and the few
/// actions that change it.
struct ClockCard: View {
    let screen: Loadable<[Int]>
    let health: Loadable<ClockHealth>
    var actions = DashboardActions()

    var body: some View {
        DashboardCard(title: "Clock", systemImage: "clock", height: DashboardCardHeight.mirror) {
            // Mirror and controls as one centred group: the mirror hugs its
            // snapped 4:1 panel, so on a wide card the spare width goes to
            // the margins rather than between the two.
            HStack(alignment: .center, spacing: 24) {
                mirror
                controls
                    .fixedSize()
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        } accessory: {
            if let at = screen.loadedAt, screen.isStale { StaleChip(since: at) }
        }
    }

    private var pixels: [Int] { screen.value ?? Array(repeating: 0, count: 256) }

    private var mirror: some View {
        MatrixScreenView(pixels: pixels)
            .ledBezel(padding: 10, cornerRadius: 10)
            .overlay {
                if screen.value == nil, !screen.isLoading {
                    Label("Clock unreachable", systemImage: "wifi.slash")
                        .font(.callout)
                        .foregroundStyle(.white.opacity(0.7))
                }
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel("Clock display")
            .accessibilityValue(appName.map { Text($0) } ?? (screen.value == nil ? Text("Not available") : Text(verbatim: "")))
    }

    private var appName: LocalizedStringResource? {
        guard let app = health.value?.device?.currentApp, !app.isEmpty else { return nil }
        return AppNames.display(app)
    }

    @ViewBuilder
    private var controls: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Showing")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                (appName.map { Text($0) } ?? Text(verbatim: "—"))
                    .font(.title3.weight(.semibold))
                    .lineLimit(1)
            }
            ControlGroup {
                button(.previous, "Previous App", "chevron.backward")
                button(.next, "Next App", "chevron.forward")
                button(.dismiss, "Dismiss Notification", "xmark")
            }
            .controlGroupStyle(.navigation)
            .labelStyle(.iconOnly)
            .fixedSize()
            if let power = health.value?.device?.matrixPower {
                Toggle("Display", isOn: Binding(get: { power }, set: { actions.clock(.power($0)) }))
                    .toggleStyle(.switch)
                    .controlSize(.small)
                    .disabled(actions.running.contains(.clock(.power(!power))))
            }
        }
    }

    private func button(_ action: ClockAction, _ title: LocalizedStringKey, _ symbol: String) -> some View {
        Button(title, systemImage: symbol) { actions.clock(action) }
            .help(title)
            .disabled(screen.value == nil || actions.running.contains(.clock(action)))
    }
}
