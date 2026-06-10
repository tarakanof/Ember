import SwiftUI

/// Sidebar settings shell. The sidebar floats with Liquid Glass automatically on
/// the macOS 26 SDK — no explicit glass modifier. Detail frame hints pre-empt the
/// common undersized-window issue.
struct SettingsRootView: View {
    @State private var selection: SettingsPane? = .connection

    var body: some View {
        NavigationSplitView {
            List(SettingsPane.allCases, id: \.self, selection: $selection) { pane in
                Label(pane.title, systemImage: pane.systemImage)
            }
            .navigationSplitViewColumnWidth(min: 170, ideal: 190, max: 220)
            .navigationTitle("Ember Settings")
        } detail: {
            detail(for: selection ?? .connection)
                .frame(minWidth: 460, idealWidth: 500, minHeight: 360)
        }
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
        case .app:        AppTab()
        }
    }
}
