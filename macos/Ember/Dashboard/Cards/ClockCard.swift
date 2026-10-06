import SwiftUI
import EmberKit

struct ClockCard: View {
    let screen: Loadable<[Int]>
    var actions = DashboardActions()
    var disabledNotice: LocalizedStringResource?

    var body: some View {
        DashboardCard(title: "Clock", systemImage: "clock", height: DashboardCardHeight.mirror) {
            HStack(alignment: .center, spacing: 24) {
                mirror
                controls
                    .fixedSize()
                    .disabled(disabledNotice != nil)
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
                if let disabledNotice {
                    Label(String(localized: disabledNotice), systemImage: "clock.badge.xmark")
                        .font(.callout)
                        .foregroundStyle(.white.opacity(0.7))
                } else if screen.value == nil, !screen.isLoading {
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
                let pending = actions.pendingDisplayPower
                HStack(spacing: 6) {
                    Toggle("Display", isOn: Binding(get: { pending ?? power }, set: { actions.clock(.power($0)) }))
                        .toggleStyle(.switch)
                        .controlSize(.small)
                        .disabled(pending != nil)
                    if pending != nil { ProgressView().controlSize(.mini) }
                }
            }
        }
    }

    private func button(_ action: ClockAction, _ title: LocalizedStringKey, _ symbol: String) -> some View {
        Button(title, systemImage: symbol) { actions.clock(action) }
            .help(title)
            .disabled(screen.value == nil || actions.running.contains(.clock(action)))
    }
}
