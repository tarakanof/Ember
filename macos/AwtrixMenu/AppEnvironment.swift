import Foundation
import AwtrixMenuKit

/// App-wide coordinator: owns the producer.env path, the live APIClient, the
/// AppModel (polled status/pomodoro), and a PomodoroService for menu actions.
/// `reloadConnection()` re-reads producer.env, rebuilds the client, and
/// reconfigures the model — so Connection-tab saves take effect without relaunch.
@MainActor
@Observable
public final class AppEnvironment {
    public let model = AppModel()
    public private(set) var pomodoro: PomodoroService

    let producerEnvPath: URL

    public init(producerEnvPath: URL = AppEnvironment.defaultEnvPath) {
        self.producerEnvPath = producerEnvPath
        let client = AppEnvironment.makeClient(path: producerEnvPath)
        pomodoro = PomodoroService(client: client)
        model.configure(client: client)
        model.startPolling()   // begin polling at launch (idempotent); self-started
                               // here so the menu-bar label updates without opening
                               // the popover first.
    }

    /// Re-read producer.env, rebuild the client, reconfigure model + service.
    public func reloadConnection() {
        let client = AppEnvironment.makeClient(path: producerEnvPath)
        pomodoro = PomodoroService(client: client)
        model.configure(client: client)
        Task { await model.refresh() }
    }

    /// Reads producer.env from disk (missing file -> empty env -> Offline client).
    public func currentEnv() -> EnvFile {
        let text = (try? String(contentsOf: producerEnvPath, encoding: .utf8)) ?? ""
        return EnvFile(parsing: text)
    }

    static func makeClient(path: URL) -> APIClient {
        let text = (try? String(contentsOf: path, encoding: .utf8)) ?? ""
        return APIClient(producerEnv: EnvFile(parsing: text))
    }

    public static var defaultEnvPath: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/awtrix-ai-status/producer.env")
    }
}
