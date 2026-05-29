import SwiftUI
import AwtrixMenuKit

@main
struct AwtrixMenuApp: App {
    @State private var model = AppModel()

    init() {
        let m = AppModel()
        m.configure(client: Bootstrap.makeClient())
        m.startPolling()
        _model = State(initialValue: m)
    }

    var body: some Scene {
        MenuBarExtra("AWTRIX", systemImage: "square.grid.3x3.fill") {
            Text(model.connected ? "Connected" : "Offline")
            Divider()
            Button("Quit") { NSApplication.shared.terminate(nil) }
        }
        .menuBarExtraStyle(.window)
    }
}
