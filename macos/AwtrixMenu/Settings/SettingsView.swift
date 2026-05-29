import SwiftUI

struct SettingsView: View {
    var body: some View {
        TabView {
            ConnectionTab()
                .tabItem { Label("Connection", systemImage: "network") }
            DisplayTab()
                .tabItem { Label("Display", systemImage: "rectangle.dashed") }
            PomodoroTab()
                .tabItem { Label("Pomodoro", systemImage: "timer") }
            AppTab()
                .tabItem { Label("App", systemImage: "app.badge") }
        }
        .frame(width: 460, height: 420)
    }
}
