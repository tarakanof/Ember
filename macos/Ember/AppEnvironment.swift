import AppKit
import Foundation
import OSLog
import SwiftUI
import EmberKit

/// App-wide coordinator: owns the producer.env path, the live APIClient and
/// everything built on it: `live` (every polled feed), `actions` (user
/// actions), `settings` (config models) and the per-endpoint services the
/// settings panes call. `reloadConnection()` re-reads producer.env and points
/// all of it at the new server, so Connection saves apply without relaunch.
@MainActor
@Observable
public final class AppEnvironment {
    /// Every live feed; the menu, the Dashboard, the Dock menu and the bot
    /// read it. Views hold tier C feeds with `.task { await live.track(…) }`.
    public let live = LiveModel()
    /// Runs Pomodoro, clock and app-visibility actions and keeps the last error.
    public let actions: ActionRunner
    /// One auto-saving config model per settings area.
    public let settings: SettingsModels
    /// The configured server, nil when producer.env has none.
    public private(set) var serverURL: URL?
    public private(set) var pomodoro: PomodoroService
    public private(set) var stats: StatsService
    public private(set) var health: HealthService
    public private(set) var activity: ActivityService
    public private(set) var preview: PreviewService
    public private(set) var weather: WeatherService
    public private(set) var usage: UsageService
    public private(set) var quiet: QuietService
    public private(set) var displayConfig: DisplayService
    public private(set) var device: DeviceService
    /// The clock's settings for Settings › Clock and Sounds (⌘R reloads it).
    public let deviceSettings: DeviceSettingsModel
    public private(set) var meetings: MeetingsService
    public private(set) var reminderWatcher: ReminderWatcher
    public let location = LocationService()
    public let serverDiscovery = ServerDiscovery()
    public let producers: ProducerInstallService

    /// Menu-only prefs (icon palette + tray glyphs), persisted to UserDefaults.
    /// Observed so the menu-bar label updates live when the App tab edits them.
    public var prefs: MenuPrefs {
        didSet {
            AppEnvironment.savePrefs(prefs)
            AppEnvironment.applyAppIcon(prefs.appIcon)
            BotAnimator.shared.showInMenuBar(prefs.trayStyle == "bot")
        }
    }

    private static let log = Logger(subsystem: "com.ember.Ember", category: "app")

    static let prefsDefaults = UserDefaults.standard
    static let lastReconciledVersionKey = "producers.lastReconciledVersion"

    static func loadPrefs() -> MenuPrefs {
        let d = prefsDefaults
        return MenuPrefs(
            appIcon: d.string(forKey: "appIcon") ?? MenuPrefs.default.appIcon,
            trayClaudeGlyph: d.string(forKey: "trayClaudeGlyph") ?? MenuPrefs.default.trayClaudeGlyph,
            trayCodexGlyph: d.string(forKey: "trayCodexGlyph") ?? MenuPrefs.default.trayCodexGlyph,
            trayIdleGlyph: d.string(forKey: "trayIdleGlyph") ?? MenuPrefs.default.trayIdleGlyph,
            trayStyle: d.string(forKey: "trayStyle") ?? MenuPrefs.default.trayStyle,
            trayTint: d.string(forKey: "trayTint") ?? MenuPrefs.default.trayTint
        ).validated()
    }

    static func savePrefs(_ p: MenuPrefs) {
        let d = prefsDefaults
        d.set(p.appIcon, forKey: "appIcon")
        d.set(p.trayClaudeGlyph, forKey: "trayClaudeGlyph")
        d.set(p.trayCodexGlyph, forKey: "trayCodexGlyph")
        d.set(p.trayIdleGlyph, forKey: "trayIdleGlyph")
        d.set(p.trayStyle, forKey: "trayStyle")
        d.set(p.trayTint, forKey: "trayTint")
    }

    /// Pushes the winning session's state into the bot, re-arming on each change.
    /// Done here rather than in `MenuBarLabel`: a MenuBarExtra label doesn't run
    /// `onChange`/`onAppear`, and the Dock bot needs the state regardless.
    private func feedBot() {
        let state = withObservationTracking {
            live.winningSession?.state ?? "idle"
        } onChange: { [weak self] in
            Task { @MainActor in self?.feedBot() }
        }
        BotAnimator.shared.setState(state)
    }

    /// Applies the chosen Ember icon as the runtime Dock icon (visible only while
    /// a window is open — see AppDelegate). No-op if the asset is missing.
    static func applyAppIcon(_ palette: String) {
        if palette == "bot" {
            BotAnimator.shared.showInDock(true)
            return
        }
        BotAnimator.shared.showInDock(false)
        if let img = NSImage(named: "appicon-\(palette)") {
            NSApplication.shared.applicationIconImage = img
        }
    }

    let producerEnvPath: URL

    /// The scene's `openWindow`, captured by the first window or menu that
    /// appears, for AppKit callers (the Dock menu) that have no environment.
    @ObservationIgnored var openWindowAction: OpenWindowAction?
    /// The one writer queue for producer.env.
    public let envStore: EnvFileStore
    @ObservationIgnored private var sleepObservers: [NSObjectProtocol] = []

    public init(producerEnvPath: URL = AppEnvironment.defaultEnvPath) {
        self.producerEnvPath = producerEnvPath
        prefs = AppEnvironment.loadPrefs()
        let client = AppEnvironment.makeClient(path: producerEnvPath)
        actions = ActionRunner(live: live)
        envStore = EnvFileStore(path: producerEnvPath)
        settings = SettingsModels(client: client, envStore: envStore)
        serverURL = client.baseURL
        pomodoro = PomodoroService(client: client)
        stats = StatsService(client: client)
        health = HealthService(client: client)
        activity = ActivityService(client: client)
        preview = PreviewService(client: client)
        weather = WeatherService(client: client)
        usage = UsageService(client: client)
        quiet = QuietService(client: client)
        displayConfig = DisplayService(client: client)
        device = DeviceService(client: client)
        deviceSettings = DeviceSettingsModel(service: DeviceService(client: client))
        meetings = MeetingsService(client: client)
        reminderWatcher = ReminderWatcher(client: client)
        producers = ProducerInstallService(
            sm: RealSMAppService(),
            runner: ProcessCommandRunner(),
            bundleMacOSDir: Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS"),
            home: FileManager.default.homeDirectoryForCurrentUser,
            fileExists: { FileManager.default.fileExists(atPath: $0) }
        )
        live.configure(client: client)
        actions.configure(client: client)
        settings.connectionEnv.onSaved = { [weak self] _ in self?.reloadConnection() }
        // Polls tiers A and B from launch so the menu-bar label is live
        // without opening the menu first.
        live.start()
        observeSleep()
        reminderWatcher.start()
        serverDiscovery.start()
        AppEnvironment.applyAppIcon(prefs.appIcon)
        BotAnimator.shared.showInMenuBar(prefs.trayStyle == "bot")
        feedBot()
        reconcileProducers()
    }

    /// Best-effort launch check of the producer LaunchAgents (#142), off the
    /// main thread. After an app update (a new bundle fingerprint: version,
    /// build and a digest of the bundled helpers) every enabled agent is
    /// re-registered so the new helpers take over; otherwise only an enabled
    /// agent launchd has no job for is. The fingerprint is recorded only when
    /// every re-registration succeeded, so a failure retries next launch.
    private func reconcileProducers() {
        let bundle = Bundle.main
        let version = bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
        let build = bundle.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? ""
        let appURL = bundle.bundleURL
        let producers = self.producers
        let key = Self.lastReconciledVersionKey
        let log = Self.log
        Task.detached(priority: .utility) {
            let defaults = UserDefaults.standard
            let fingerprint = bundleFingerprint(appURL: appURL, version: version, build: build)
            let changed = shouldReconcileAfterUpdate(currentVersion: fingerprint,
                                                     lastReconciledVersion: defaults.string(forKey: key))
            let outcomes = await producers.reconcile(bundleChanged: changed)
            for outcome in outcomes {
                if let error = outcome.error {
                    log.error("producer re-register failed: agent=\(outcome.agent.rawValue, privacy: .public) reason=\(String(describing: outcome.reason), privacy: .public) error=\(error.localizedDescription, privacy: .public)")
                } else {
                    log.info("producer re-registered: agent=\(outcome.agent.rawValue, privacy: .public) reason=\(String(describing: outcome.reason), privacy: .public)")
                }
            }
            if shouldRecordFingerprint(bundleChanged: changed, outcomes: outcomes) {
                defaults.set(fingerprint, forKey: key)
            }
        }
    }

    /// Re-read producer.env, rebuild the client, reconfigure everything on it.
    public func reloadConnection() {
        let client = AppEnvironment.makeClient(path: producerEnvPath)
        serverURL = client.baseURL
        pomodoro = PomodoroService(client: client)
        stats = StatsService(client: client)
        health = HealthService(client: client)
        activity = ActivityService(client: client)
        preview = PreviewService(client: client)
        weather = WeatherService(client: client)
        usage = UsageService(client: client)
        quiet = QuietService(client: client)
        displayConfig = DisplayService(client: client)
        device = DeviceService(client: client)
        deviceSettings.configure(service: device)
        meetings = MeetingsService(client: client)
        reminderWatcher.reconfigure(client: client)
        live.configure(client: client)
        actions.configure(client: client)
        settings.configure(client: client)
    }

    /// Pauses polling while the Mac sleeps; wake refetches everything at once.
    private func observeSleep() {
        let nc = NSWorkspace.shared.notificationCenter
        sleepObservers = [
            nc.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.live.pause() }
            },
            nc.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.live.resume() }
            },
        ]
    }

    /// Opens a window by scene id in front of other apps. No-op until a
    /// scene has captured `openWindowAction`.
    func openWindow(id: String) {
        guard let openWindowAction else { return }
        presentWindow(id: id, using: openWindowAction)
    }

    /// Whether the Settings window is on screen (⌘R reloads settings only then).
    @ObservationIgnored var isSettingsOpen = false

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
