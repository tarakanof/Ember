import AppKit
import Foundation
import OSLog
import SwiftUI
import EmberKit

/// App-wide coordinator: owns the server connection and the models built on it.
@MainActor
@Observable
public final class AppEnvironment {
    public let live = LiveModel()
    public let actions: ActionRunner
    public let settings: SettingsModels
    public let connection: ServerConnection
    public var serverURL: URL? { connection.serverURL }
    public var preview: PreviewService { PreviewService(client: connection.client) }
    public let deviceSettings: DeviceSettingsModel
    /// Settings › Knob: the registered knob and the boards on USB.
    public let knob: KnobModel
    /// The Dashboard's knob section: stats polled while it's visible.
    public let knobStats: KnobStatsModel
    /// A Dashboard section to scroll to when the window shows (deep links).
    public var dashboardScrollTarget: String?
    public private(set) var knobDiagnosticsSaving = false
    public private(set) var knobDiagnosticsError: String?
    public private(set) var reminderWatcher: ReminderWatcher
    public let location = LocationService()
    public let serverDiscovery = ServerDiscovery()
    public let clockDiscovery = ClockDiscovery()
    public let producers: ProducerInstallService
    public let permissions: PermissionsModel

    public var prefs: MenuPrefs {
        didSet {
            AppEnvironment.savePrefs(prefs)
            AppEnvironment.applyAppIcon(prefs.appIcon)
            BotAnimator.shared.showInMenuBar(prefs.trayStyle == "bot", colored: prefs.trayTint == "color")
        }
    }

    public private(set) var menuBarLabel = MenuRows.label(connection: .connecting, winning: nil, prefs: .default)

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

    private func feedBot() {
        let state = withObservationTracking {
            live.winningSession?.state ?? "idle"
        } onChange: { [weak self] in
            Task { @MainActor in self?.feedBot() }
        }
        BotAnimator.shared.setState(state)
    }

    private func feedMenuBarLabel() {
        let label = withObservationTracking {
            MenuRows.label(connection: live.connection, winning: live.winningSession, prefs: prefs)
        } onChange: { [weak self] in
            Task { @MainActor in self?.feedMenuBarLabel() }
        }
        if label != menuBarLabel { menuBarLabel = label }
    }

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

    @ObservationIgnored var openWindowAction: OpenWindowAction?
    public let envStore: EnvFileStore
    @ObservationIgnored private var sleepObservers: [NSObjectProtocol] = []

    public init(producerEnvPath: URL = AppEnvironment.defaultEnvPath) {
        self.producerEnvPath = producerEnvPath
        prefs = AppEnvironment.loadPrefs()
        connection = ServerConnection(envPath: producerEnvPath)
        let client = connection.client
        actions = ActionRunner(live: live, connection: connection)
        envStore = EnvFileStore(path: producerEnvPath)
        settings = SettingsModels(client: client, envStore: envStore)
        deviceSettings = DeviceSettingsModel(service: connection.device, live: live)
        knob = KnobModel(service: KnobService(client: client))
        knobStats = KnobStatsModel(service: KnobService(client: client))
        let watcher = ReminderWatcher(client: client)
        reminderWatcher = watcher
        producers = ProducerInstallService(
            sm: RealSMAppService(),
            runner: ProcessCommandRunner(),
            bundleMacOSDir: Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS"),
            home: FileManager.default.homeDirectoryForCurrentUser,
            fileExists: { FileManager.default.fileExists(atPath: $0) },
            prefs: UserDefaultsProducerPrefs()
        )
        permissions = PermissionsModel(sources: AppPermissionSources(
            connection: connection, producers: producers, reminders: watcher, location: location))
        #if DEBUG
        // A snapshot run only draws fixtures: no server, producers or USB.
        if KnobSnapshotRenderer.isRequested { return }
        #endif
        live.configure(client: client)
        settings.connectionEnv.onSaved = { [weak self] _ in self?.reloadConnection() }
        live.start()
        observeSleep()
        reminderWatcher.start()
        AppEnvironment.applyAppIcon(prefs.appIcon)
        BotAnimator.shared.showInMenuBar(prefs.trayStyle == "bot", colored: prefs.trayTint == "color")
        feedBot()
        feedMenuBarLabel()
        reconcileProducers()
        watchKnobPorts()
    }

    /// Watches USB for the knob passively (IOKit: VID:PID and serial
    /// number). Nothing opens the port until the user opens Settings › Knob
    /// or starts a setup: idf.py monitor and esptool share it.
    private func watchKnobPorts() {
        knob.ports.start()
    }

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
            await producers.seedOptOutForNewAgents()
            let fingerprint = bundleFingerprint(appURL: appURL, version: version, build: build)
            let changed = shouldReconcileAfterUpdate(currentVersion: fingerprint,
                                                     lastReconciledVersion: defaults.string(forKey: key))
            let outcomes = await producers.reconcile(bundleChanged: changed)
            Self.logReconcile(outcomes, log: log)
            if shouldRecordFingerprint(bundleChanged: changed, outcomes: outcomes) {
                defaults.set(fingerprint, forKey: key)
            }
            if shouldRecheckAfterReconcile(bundleChanged: changed, outcomes: outcomes) {
                try? await Task.sleep(for: producerRecheckDelay)
                Self.logReconcile(await producers.reconcile(bundleChanged: false), log: log)
            }
        }
    }

    private nonisolated static func logReconcile(_ outcomes: [ReconcileOutcome], log: Logger) {
        for outcome in outcomes {
            if let error = outcome.error {
                log.error("producer re-register failed: agent=\(outcome.agent.rawValue, privacy: .public) reason=\(String(describing: outcome.reason), privacy: .public) error=\(error.localizedDescription, privacy: .public)")
            } else {
                log.notice("producer re-registered: agent=\(outcome.agent.rawValue, privacy: .public) reason=\(String(describing: outcome.reason), privacy: .public)")
            }
        }
    }

    public func reloadConnection() {
        guard connection.reload() else { return }
        let client = connection.client
        reminderWatcher.reconfigure(client: client)
        live.configure(client: client)
        settings.configure(client: client)
        deviceSettings.configure(service: connection.device)
        knob.configure(service: KnobService(client: client))
        knobStats.configure(service: KnobService(client: client))
    }

    /// Sets the knob's diagnostics level from the Dashboard, then refetches
    /// its stats so the section follows.
    func setKnobDiagnostics(_ level: KnobDiagnostics) async {
        guard !knobDiagnosticsSaving else { return }
        knobDiagnosticsSaving = true
        defer { knobDiagnosticsSaving = false }
        if !knob.isLoaded { await knob.load() }
        if await knob.setDiagnostics(level) {
            knobDiagnosticsError = nil
        } else {
            knobDiagnosticsError = knob.settings.saveError.map { String(localized: $0.message) }
                ?? String(localized: "Couldn't change the knob's diagnostics.")
        }
        if let id = knob.knob?.id { await knobStats.refresh(deviceID: id) }
    }

    private func observeSleep() {
        let nc = NSWorkspace.shared.notificationCenter
        sleepObservers = [
            nc.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated {
                    self?.live.pause()
                    self?.clockDiscovery.stop()
                }
            },
            nc.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.live.resume() }
            },
        ]
    }

    func openWindow(id: String) {
        guard let openWindowAction else { return }
        presentWindow(id: id, using: openWindowAction)
    }

    @ObservationIgnored var isSettingsOpen = false

    public static var defaultEnvPath: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/ember/producer.env")
    }
}
