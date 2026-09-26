import SwiftUI
import EmberKit

/// Sidebar settings shell. The sidebar floats with Liquid Glass on its own;
/// the window title follows the pane and the subtitle carries the one save
/// status for every model. The selected pane lives in the `settings.pane`
/// default, so it's restored on reopen and other windows can open a pane.
struct SettingsRootView: View {
    @Environment(AppEnvironment.self) private var env
    @AppStorage(SettingsPaneID.storageKey) private var paneName = SettingsPaneID.connection.rawValue
    /// The clock's settings, shared by the Clock and Sounds panes.
    @State private var device: DeviceSettingsModel?

    private var selection: Binding<SettingsPane?> {
        Binding(
            get: { SettingsPane(stored: paneName) },
            set: { paneName = ($0 ?? .connection).rawValue })
    }

    var body: some View {
        let pane = SettingsPane(stored: paneName)
        NavigationSplitView {
            List(SettingsPane.allCases, selection: selection) { pane in
                Label { Text(pane.title) } icon: { Image(systemName: pane.systemImage) }
                    .tag(pane)
            }
            .navigationSplitViewColumnWidth(min: 170, ideal: 190, max: 220)
        } detail: {
            if let device {
                detail(for: pane)
                    .environment(device)
                    .navigationTitle(Text(pane.title))
                    .navigationSubtitle(subtitle(device))
                    .frame(minWidth: 460, minHeight: 360)
            }
        }
        .onAppear {
            if device == nil { device = DeviceSettingsModel(service: env.device) }
            // A stored legacy name ("pomodoro") is rewritten to its new pane.
            if paneName != pane.rawValue { paneName = pane.rawValue }
        }
        .onChange(of: env.device) { _, service in device?.configure(service: service) }
    }

    @ViewBuilder
    private func detail(for pane: SettingsPane) -> some View {
        switch pane {
        case .general:    GeneralPane()
        case .connection: ConnectionPane()
        case .clock:      ClockPane()
        case .agents:     AgentsPane()
        case .focus:      FocusPane()
        case .weather:    WeatherPane()
        case .calendar:   CalendarPane()
        case .sounds:     SoundsPane()
        }
    }

    private func subtitle(_ device: DeviceSettingsModel) -> Text {
        let status = AggregateSaveStatus.combine((env.settings.all + device.all).map(\.status))
        return status.subtitle.map { Text($0) } ?? Text(verbatim: "")
    }
}
