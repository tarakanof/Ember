import SwiftUI

struct SettingsView: View {
    // A fixed width with a per-tab height lets the window size itself to each
    // pane — the native toolbar-settings feel — instead of pinning every tab to
    // one short frame that forces the taller forms to scroll.
    private let width: CGFloat = 500

    var body: some View {
        TabView {
            ConnectionTab()
                .frame(width: width, height: 280)
                .tabItem { Label("Connection", systemImage: "network") }
            DisplayTab()
                .frame(width: width, height: 500)
                .tabItem { Label("Display", systemImage: "rectangle.dashed") }
            PomodoroTab()
                .frame(width: width, height: 540)
                .tabItem { Label("Pomodoro", systemImage: "timer") }
            AppTab()
                .frame(width: width, height: 400)
                .tabItem { Label("App", systemImage: "app.badge") }
        }
    }
}
