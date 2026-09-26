import SwiftUI

/// Sidebar settings shell. The sidebar floats with Liquid Glass automatically on
/// the macOS 26 SDK — no explicit glass modifier. The selected pane is kept in
/// the `settings.pane` default, so it's restored on reopen and other windows
/// can open Settings on a pane.
struct SettingsRootView: View {
    @AppStorage("settings.pane") private var paneName = SettingsPane.connection.rawValue

    private var selection: Binding<SettingsPane?> {
        Binding(
            get: { SettingsPane(rawValue: paneName) ?? .connection },
            set: { paneName = ($0 ?? .connection).rawValue })
    }

    var body: some View {
        NavigationSplitView {
            List(SettingsPane.allCases, id: \.self, selection: selection) { pane in
                Label(pane.title, systemImage: pane.systemImage)
            }
            .navigationSplitViewColumnWidth(min: 170, ideal: 190, max: 220)
        } detail: {
            detail(for: selection.wrappedValue ?? .connection)
                .frame(minWidth: 460, idealWidth: 500, minHeight: 360)
        }
        .toolbar(removing: .title)
    }

    @ViewBuilder
    private func detail(for pane: SettingsPane) -> some View {
        switch pane {
        case .connection: ConnectionTab()
        case .device:     DeviceTab()
        case .display:    DisplayTab()
        case .pomodoro:   PomodoroTab()
        case .weather:    WeatherTab()
        case .reminders:  RemindersTab()
        case .meetings:   MeetingsTab()
        case .app:        AppTab()
        }
    }
}
