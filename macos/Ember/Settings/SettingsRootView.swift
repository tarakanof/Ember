import SwiftUI
import EmberKit

struct SettingsRootView: View {
    @Environment(AppEnvironment.self) private var env
    @AppStorage(SettingsPaneID.storageKey) private var paneName = SettingsPaneID.connection.rawValue

    static let windowWidth: CGFloat = 760

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
            detail(for: pane)
                .environment(env.deviceSettings)
                .navigationTitle(Text(pane.title))
                .navigationSubtitle(subtitle(env.deviceSettings))
                .frame(minWidth: 460, minHeight: 360)
        }
        .onAppear {
            if paneName != pane.rawValue { paneName = pane.rawValue }
        }
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
        case .permissions: PermissionsPane()
        }
    }

    private func subtitle(_ device: DeviceSettingsModel) -> Text {
        let status = AggregateSaveStatus.combine((env.settings.all + device.all).map(\.status))
        return status.subtitle.map { Text($0) } ?? Text(verbatim: "")
    }
}
