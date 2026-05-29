import SwiftUI

@main
struct AwtrixMenuApp: App {
    var body: some Scene {
        MenuBarExtra("AWTRIX", systemImage: "square.grid.3x3.fill") {
            Text("AWTRIX Menu — scaffold")
            Divider()
            Button("Quit") { NSApplication.shared.terminate(nil) }
        }
        .menuBarExtraStyle(.window)
    }
}
