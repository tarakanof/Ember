import AppKit
import Foundation
import EmberKit

/// App-wide coordinator: owns the producer.env path, the live APIClient, the
/// AppModel (polled status/pomodoro), and a PomodoroService for menu actions.
/// `reloadConnection()` re-reads producer.env, rebuilds the client, and
/// reconfigures the model — so Connection-tab saves take effect without relaunch.
@MainActor
@Observable
public final class AppEnvironment {
    public let model = AppModel()
    public private(set) var pomodoro: PomodoroService
    public private(set) var preview: PreviewService
    public private(set) var weather: WeatherService
    public private(set) var usage: UsageService
    public private(set) var device: DeviceService
    public private(set) var reminderWatcher: ReminderWatcher
    public let location = LocationService()
    public let serverDiscovery = ServerDiscovery()

    /// Menu-only prefs (icon palette + tray glyphs), persisted to UserDefaults.
    /// Observed so the menu-bar label updates live when the App tab edits them.
    public var prefs: MenuPrefs {
        didSet {
            AppEnvironment.savePrefs(prefs)
            AppEnvironment.applyAppIcon(prefs.appIcon)
        }
    }

    static let prefsDefaults = UserDefaults.standard

    static func loadPrefs() -> MenuPrefs {
        let d = prefsDefaults
        return MenuPrefs(
            appIcon: d.string(forKey: "appIcon") ?? MenuPrefs.default.appIcon,
            trayClaudeGlyph: d.string(forKey: "trayClaudeGlyph") ?? MenuPrefs.default.trayClaudeGlyph,
            trayCodexGlyph: d.string(forKey: "trayCodexGlyph") ?? MenuPrefs.default.trayCodexGlyph,
            trayIdleGlyph: d.string(forKey: "trayIdleGlyph") ?? MenuPrefs.default.trayIdleGlyph
        ).validated()
    }

    static func savePrefs(_ p: MenuPrefs) {
        let d = prefsDefaults
        d.set(p.appIcon, forKey: "appIcon")
        d.set(p.trayClaudeGlyph, forKey: "trayClaudeGlyph")
        d.set(p.trayCodexGlyph, forKey: "trayCodexGlyph")
        d.set(p.trayIdleGlyph, forKey: "trayIdleGlyph")
    }

    /// Applies the chosen Ember icon as the runtime Dock icon (visible only while
    /// a window is open — see AppDelegate). No-op if the asset is missing.
    static func applyAppIcon(_ palette: String) {
        if let img = NSImage(named: "appicon-\(palette)") {
            NSApplication.shared.applicationIconImage = img
        }
    }

    let producerEnvPath: URL

    public init(producerEnvPath: URL = AppEnvironment.defaultEnvPath) {
        self.producerEnvPath = producerEnvPath
        prefs = AppEnvironment.loadPrefs()
        let client = AppEnvironment.makeClient(path: producerEnvPath)
        pomodoro = PomodoroService(client: client)
        preview = PreviewService(client: client)
        weather = WeatherService(client: client)
        usage = UsageService(client: client)
        device = DeviceService(client: client)
        reminderWatcher = ReminderWatcher(client: client)
        model.configure(client: client)
        model.startPolling()   // begin polling at launch (idempotent); self-started
                               // here so the menu-bar label updates without opening
                               // the popover first.
        reminderWatcher.start()
        serverDiscovery.start()
        AppEnvironment.applyAppIcon(prefs.appIcon)
    }

    /// Re-read producer.env, rebuild the client, reconfigure model + service.
    public func reloadConnection() {
        let client = AppEnvironment.makeClient(path: producerEnvPath)
        pomodoro = PomodoroService(client: client)
        preview = PreviewService(client: client)
        weather = WeatherService(client: client)
        usage = UsageService(client: client)
        device = DeviceService(client: client)
        reminderWatcher.reconfigure(client: client)
        model.configure(client: client)
        Task { await model.refresh() }
    }

    /// Reads producer.env from disk (missing file -> empty env -> Offline client).
    public func currentEnv() -> EnvFile {
        let text = (try? String(contentsOf: producerEnvPath, encoding: .utf8)) ?? ""
        return EnvFile(parsing: text)
    }

    /// Best-effort fetch of the connected server's build (`GET /version`, no auth).
    /// Returns nil when the server is unreachable/unconfigured.
    public func serverVersion() async -> String? {
        let client = AppEnvironment.makeClient(path: producerEnvPath)
        let info: VersionInfo? = try? await client.get("/version")
        return info?.short
    }

    static func makeClient(path: URL) -> APIClient {
        let text = (try? String(contentsOf: path, encoding: .utf8)) ?? ""
        return APIClient(producerEnv: EnvFile(parsing: text))
    }

    public static var defaultEnvPath: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/ember/producer.env")
    }
}
