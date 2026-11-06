import Foundation
import Observation
import OSLog

@MainActor
public struct ReminderPrefsLegacyStore {
    public var local: () -> ReminderPrefs?
    public var isMigrated: () -> Bool
    public var markMigrated: () -> Void

    public init(local: @escaping () -> ReminderPrefs?, isMigrated: @escaping () -> Bool,
                markMigrated: @escaping () -> Void) {
        self.local = local
        self.isMigrated = isMigrated
        self.markMigrated = markMigrated
    }

    public static func userDefaults(_ defaults: UserDefaults = .standard) -> ReminderPrefsLegacyStore {
        ReminderPrefsLegacyStore(
            local: { defaults.data(forKey: "reminderPrefs").flatMap { ReminderPrefs(legacyData: $0) } },
            isMigrated: { defaults.bool(forKey: "reminderPrefsMigratedToServer") },
            markMigrated: { defaults.set(true, forKey: "reminderPrefsMigratedToServer") })
    }
}

public func reminderPrefsToMigrate(local: ReminderPrefs?, server: ReminderPrefs) -> ReminderPrefs? {
    guard server == ReminderPrefs(), let local = local?.clampedForServer, local != server else { return nil }
    return local
}

@MainActor
public final class ReminderPrefsSync {
    private let model: @MainActor () -> ServerConfigModel<ReminderPrefs>
    private let legacy: ReminderPrefsLegacyStore
    private let apply: @MainActor (ReminderPrefs?) -> Void
    private let saveIfDefault: @MainActor (ReminderPrefs) async throws -> ReminderPrefs?
    private let sleep: @Sendable (Duration) async throws -> Void
    private var loop: Task<Void, Never>?
    private var migrating = false

    static let refreshInterval: Duration = .seconds(300)
    static let retryInterval: Duration = .seconds(30)

    private static let log = Logger(subsystem: "com.ember.Ember", category: "reminders")

    public convenience init(model: @escaping @MainActor () -> ServerConfigModel<ReminderPrefs>,
                            legacy: ReminderPrefsLegacyStore,
                            saveIfDefault: @escaping @MainActor (ReminderPrefs) async throws -> ReminderPrefs?,
                            apply: @escaping @MainActor (ReminderPrefs?) -> Void) {
        self.init(model: model, legacy: legacy, saveIfDefault: saveIfDefault, apply: apply,
                  sleep: { try await Task.sleep(for: $0) })
    }

    init(model: @escaping @MainActor () -> ServerConfigModel<ReminderPrefs>,
         legacy: ReminderPrefsLegacyStore,
         saveIfDefault: @escaping @MainActor (ReminderPrefs) async throws -> ReminderPrefs?,
         apply: @escaping @MainActor (ReminderPrefs?) -> Void,
         sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.model = model
        self.legacy = legacy
        self.saveIfDefault = saveIfDefault
        self.apply = apply
        self.sleep = sleep
    }

    public func start() {
        observe()
        guard loop == nil else { return }
        loop = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                let loaded = await self.refresh()
                try? await self.sleep(loaded ? Self.refreshInterval : Self.retryInterval)
            }
        }
    }

    public func stop() {
        loop?.cancel()
        loop = nil
    }

    @discardableResult
    func refresh() async -> Bool {
        let m = model()
        await m.load()
        await migrate(m)
        return m.isLoaded
    }

    func observe() {
        let applied = withObservationTracking {
            model().applied
        } onChange: { [weak self] in
            Task { @MainActor in self?.observe() }
        }
        apply(applied)
    }

    func migrate(_ m: ServerConfigModel<ReminderPrefs>) async {
        guard !migrating, !legacy.isMigrated(), let server = m.applied, !m.hasUnsavedChanges else { return }
        migrating = true
        defer { migrating = false }
        guard let local = reminderPrefsToMigrate(local: legacy.local(), server: server) else {
            legacy.markMigrated()
            return
        }
        do {
            if let saved = try await saveIfDefault(local) {
                if !m.hasUnsavedChanges { m.reset(to: saved) }
                Self.log.info("reminder prefs moved to the server")
            } else {
                await m.load()
                Self.log.info("reminder prefs not moved: the server already has other values")
            }
            legacy.markMigrated()
        } catch {
            Self.log.error("reminder prefs migration failed; retrying on the next load: \(error.localizedDescription, privacy: .public)")
        }
    }
}
