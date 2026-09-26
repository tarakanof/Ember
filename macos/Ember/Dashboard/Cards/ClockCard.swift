import SwiftUI
import EmberKit

/// Card 1: the clock's real LED matrix and the few actions that change it.
/// No "Showing <app>" label: the only source is clock health's
/// `current_app`, cached up to 30 s on the server and polled every 15 s,
/// while the clock rotates apps every few seconds and the mirror updates
/// each second — the label was wrong most of the time. NG's screen
/// endpoint carries no app name, and an extra per-second request is too
/// much for the clock's lossy link, so the mirror speaks for itself.
struct ClockCard: View {
    let screen: Loadable<[Int]>
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
            .accessibilityValue(screen.value == nil ? Text("Not available") : Text(verbatim: ""))
    }

    @ViewBuilder
    private var controls: some View {
        VStack(alignment: .leading, spacing: 10) {
            ControlGroup {
                button(.previous, "Previous App", "chevron.backward")
                button(.next, "Next App", "chevron.forward")
                button(.dismiss, "Dismiss Notification", "xmark")
            }
            .controlGroupStyle(.navigation)
            .labelStyle(.iconOnly)
            .fixedSize()
            if let power = actions.displayPower {
                Toggle("Display", isOn: Binding(get: { power }, set: { actions.clock(.power($0)) }))
                    .toggleStyle(.switch)
                    .controlSize(.small)
                    .disabled(actions.running.contains(.clock(.power(true)))
                              || actions.running.contains(.clock(.power(false))))
            }
        }
    }

    private func button(_ action: ClockAction, _ title: LocalizedStringKey, _ symbol: String) -> some View {
        Button(title, systemImage: symbol) { actions.clock(action) }
            .help(title)
            .disabled(screen.value == nil || actions.running.contains(.clock(action)))
    }
}
