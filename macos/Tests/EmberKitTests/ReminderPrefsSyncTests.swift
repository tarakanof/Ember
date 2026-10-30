import Testing
import Foundation
@testable import EmberKit

private final class FakePrefsServer: @unchecked Sendable {
    private let lock = NSLock()
    private var _prefs: ReminderPrefs
    private var _puts = 0
    private var _down = false
    private var _failPuts = false

    init(_ prefs: ReminderPrefs = ReminderPrefs()) { _prefs = prefs }

    var prefs: ReminderPrefs {
        get { lock.withLock { _prefs } }
        set { lock.withLock { _prefs = newValue } }
    }
    var puts: Int { lock.withLock { _puts } }
    var down: Bool {
        get { lock.withLock { _down } }
        set { lock.withLock { _down = newValue } }
    }
    var failPuts: Bool {
        get { lock.withLock { _failPuts } }
        set { lock.withLock { _failPuts = newValue } }
    }

    @MainActor func model() -> ServerConfigModel<ReminderPrefs> {
        ServerConfigModel(initial: ReminderPrefs(),
                          load: { [self] in
                              if down { throw APIError.transport("offline") }
                              return prefs
                          },
                          save: { [self] value in
                              try lock.withLock {
                                  _puts += 1
                                  if _failPuts { throw APIError.http(status: 502, body: "") }
                                  _prefs = value
                              }
                          },
                          debounce: .zero, savedHold: .zero, sleep: { _ in })
    }

    func saveIfDefault(_ value: ReminderPrefs) async throws -> ReminderPrefs? {
        await Task.yield()
        return try lock.withLock {
            _puts += 1
            if _failPuts { throw APIError.http(status: 502, body: "") }
            guard _prefs == ReminderPrefs() else { return nil }
            _prefs = value
            return value
        }
    }
}

@MainActor
private final class FakeLegacy {
    var local: ReminderPrefs?
    var migrated = false

    init(_ local: ReminderPrefs?) { self.local = local }

    var store: ReminderPrefsLegacyStore {
        ReminderPrefsLegacyStore(local: { self.local }, isMigrated: { self.migrated },
                                 markMigrated: { self.migrated = true })
    }
}

@MainActor
private func makeSync(_ server: FakePrefsServer, _ model: ServerConfigModel<ReminderPrefs>, _ legacy: FakeLegacy,
                      applied: @escaping @MainActor (ReminderPrefs?) -> Void = { _ in }) -> ReminderPrefsSync {
    ReminderPrefsSync(model: { model }, legacy: legacy.store, saveIfDefault: { try await server.saveIfDefault($0) },
                      apply: applied, sleep: { _ in })
}

private let local = ReminderPrefs(enabled: true, sound: false, leadMinutes: 5, popupDuration: 20,
                                  useNativeIcon: true, nativeIconId: "77", hold: false, repeatSound: false)

@MainActor @Test func localPrefsMoveToADefaultServerOnce() async {
    let server = FakePrefsServer()
    let legacy = FakeLegacy(local)
    await makeSync(server, server.model(), legacy).refresh()
    #expect(server.prefs == local)
    #expect(server.puts == 1)
    #expect(legacy.migrated)

    server.prefs = ReminderPrefs()
    await makeSync(server, server.model(), legacy).refresh()
    #expect(server.prefs == ReminderPrefs())
    #expect(server.puts == 1)
}

@MainActor @Test func nonDefaultServerPrefsAreNeverOverwritten() async {
    let theirs = ReminderPrefs(enabled: true, leadMinutes: 10)
    let server = FakePrefsServer(theirs)
    let legacy = FakeLegacy(local)
    let model = server.model()
    await makeSync(server, model, legacy).refresh()
    #expect(server.prefs == theirs)
    #expect(server.puts == 0)
    #expect(model.applied == theirs)
    #expect(legacy.migrated)
}

@MainActor @Test func aMacWithoutLocalPrefsOnlySetsTheMarker() async {
    let server = FakePrefsServer()
    let legacy = FakeLegacy(nil)
    await makeSync(server, server.model(), legacy).refresh()
    #expect(server.puts == 0)
    #expect(legacy.migrated)
}

@MainActor @Test func aFailedMigrationKeepsTheMarkerOffAndRetries() async {
    let server = FakePrefsServer()
    server.failPuts = true
    let legacy = FakeLegacy(local)
    let model = server.model()
    let sync = makeSync(server, model, legacy)
    await sync.refresh()
    #expect(!legacy.migrated)
    #expect(model.draft == ReminderPrefs())
    #expect(!model.hasUnsavedChanges)
    #expect(server.prefs == ReminderPrefs())

    server.failPuts = false
    await sync.refresh()
    #expect(server.prefs == local)
    #expect(legacy.migrated)
}

@MainActor @Test func twoMacsMigratingAtOnceKeepOneMacsValues() async {
    let server = FakePrefsServer()
    var other = local
    other.leadMinutes = 20
    let (m4, m5) = (server.model(), server.model())
    let (l4, l5) = (FakeLegacy(local), FakeLegacy(other))
    let (s4, s5) = (makeSync(server, m4, l4), makeSync(server, m5, l5))
    await m4.load()
    await m5.load()
    async let a: Void = s4.migrate(m4)
    async let b: Void = s5.migrate(m5)
    _ = await (a, b)
    #expect(server.prefs == local || server.prefs == other)
    #expect(m4.applied == server.prefs)
    #expect(m5.applied == server.prefs)
    #expect(l4.migrated && l5.migrated)
}

@MainActor @Test func migrationClampsValuesTheServerWouldReject() async {
    let server = FakePrefsServer()
    var odd = local
    odd.leadMinutes = 45
    odd.popupDuration = 0
    odd.nativeIconId = " " + String(repeating: "9", count: 80)
    await makeSync(server, server.model(), FakeLegacy(odd)).refresh()
    #expect(server.prefs.leadMinutes == 30)
    #expect(server.prefs.popupDuration == 1)
    #expect(server.prefs.nativeIconId == String(repeating: "9", count: 64))
}

@MainActor @Test func prefsReachTheSchedulerOnlyOnceLoadedAndSurviveAnOutage() async {
    let theirs = ReminderPrefs(enabled: true, leadMinutes: 3)
    let server = FakePrefsServer(theirs)
    server.down = true
    let legacy = FakeLegacy(nil)
    let model = server.model()
    var applied: [ReminderPrefs?] = []
    let sync = makeSync(server, model, legacy) { applied.append($0) }
    sync.observe()
    await sync.refresh()
    #expect(applied == [nil])
    #expect(!legacy.migrated)

    server.down = false
    await sync.refresh()
    await until { applied.last == theirs }
    #expect(applied.last == theirs)

    server.down = true
    await sync.refresh()
    #expect(model.loadError != nil)
    #expect(applied.last == theirs)
}

@MainActor @Test func aSchedulerFedBySyncFiresOnlyAfterLoad() async {
    let server = FakePrefsServer(ReminderPrefs(enabled: true))
    server.down = true
    let model = server.model()
    let source = AlwaysOnSource()
    let scheduler = ReminderScheduler(source: source, prefs: nil, send: { _ in }, now: { Date() },
                                      uptime: { 0 }, sleep: { _ in try await Task.sleep(for: .seconds(3600)) })
    let sync = makeSync(server, model, FakeLegacy(nil)) { scheduler.prefs = $0 }
    sync.observe()
    await sync.refresh()
    #expect(!scheduler.isRunning)
    server.down = false
    await sync.refresh()
    await until { scheduler.isRunning }
    #expect(scheduler.isRunning)
    scheduler.prefs = nil
}

private struct AlwaysOnSource: ReminderSource {
    var hasAccess: Bool { true }
    func dueTimedReminders() async -> [DueReminder] { [] }
}

@MainActor
private func until(_ condition: () -> Bool) async {
    for _ in 0..<200 where !condition() { await Task.yield() }
}
