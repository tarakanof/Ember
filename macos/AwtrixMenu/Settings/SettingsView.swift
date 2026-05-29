import SwiftUI

struct SettingsView: View {
    var body: some View {
        TabView {
            ConnectionTab()
                .tabItem { Label("Connection", systemImage: "network") }
            PomodoroTab()
                .tabItem { Label("Pomodoro", systemImage: "timer") }
        }
        .frame(width: 460, height: 360)
    }
}
