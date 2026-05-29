import SwiftUI
import AwtrixMenuKit

@main
struct AwtrixMenuApp: App {
    @State private var model = AppModel()
    private let pomodoro: PomodoroService
    private var prefs = AppStoragePrefs()

    init() {
        let client = Bootstrap.makeClient()
        let m = AppModel()
        m.configure(client: client)
        m.startPolling()
        _model = State(initialValue: m)
        pomodoro = PomodoroService(client: client)
    }

    var body: some Scene {
        MenuBarExtra {
            MenuBarContentView(model: model, pomodoro: pomodoro)
        } label: {
            MenuBarLabel(session: model.winningSession, prefs: prefs.menuPrefs)
        }
        .menuBarExtraStyle(.window)
    }
}
