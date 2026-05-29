import SwiftUI

struct SettingsView: View {
    var body: some View {
        TabView {
            ConnectionTab()
                .tabItem { Label("Connection", systemImage: "network") }
        }
        .frame(width: 460, height: 300)
    }
}
