import Testing
import Foundation
@testable import EmberKit

/// A fake store: scripted load value, a queue of errors to throw, and a log
/// of saved values.
private final class Store: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: Int
    private var _saved: [Int] = []
    private var _saveErrors: [Error] = []
    private var _loadErrors: [Error] = []
    private var _loads = 0
    init(_ v: Int) { _value = v }

    var saved: [Int] { lock.withLock { _saved } }
    var loads: Int { lock.withLock { _loads } }
    func failSaves(_ errors: [Error]) { lock.withLock { _saveErrors = errors } }
    func failLoads(_ errors: [Error]) { lock.withLock { _loadErrors = errors } }

    func load() throws -> Int {
        try lock.withLock {
            _loads += 1
            if !_loadErrors.isEmpty { throw _loadErrors.removeFirst() }
            return _value
        }
    }

    func save(_ v: Int) throws {
        try lock.withLock {
            if !_saveErrors.isEmpty { throw _saveErrors.removeFirst() }
            _saved.append(v)
            _value = v
        }
    }
}

@MainActor
private func makeModel(_ store: Store) -> (ConfigModel<Int>, ManualClock) {
    let clock = ManualClock()
    let m = ConfigModel<Int>(initial: 0,
                             load: { try store.load() },
                             save: { try store.save($0) },
                             debounce: .milliseconds(600),
                             savedHold: .seconds(2),
                             sleep: clock.sleepFn)
    return (m, clock)
}

private let throttled = APIError.rateLimited(retryAfter: .seconds(1))

@MainActor @Test func loadFillsDraftAndApplied() async {
    let store = Store(7)
    let (m, _) = makeModel(store)
    #expect(!m.isLoaded)
    await m.load()
    #expect(m.draft == 7)
    #expect(m.applied == 7)
    #expect(m.isLoaded)
    #expect(!m.hasUnsavedChanges)
}

@MainActor @Test func nothingIsSavedBeforeTheFirstLoad() async {
    let store = Store(7)
    let (m, clock) = makeModel(store)
    m.draft = 3
    m.scheduleSave()
    await clock.advance(by: .seconds(1))
    await m.saveNow()
    #expect(store.saved.isEmpty)
}

@MainActor @Test func editsAreDebouncedIntoOneSave() async {
    let store = Store(1)
    let (m, clock) = makeModel(store)
    await m.load()
    for v in 2...5 {
        m.draft = v
        m.scheduleSave()
        await clock.advance(by: .milliseconds(300))
    }
    #expect(store.saved.isEmpty)
    await clock.advance(by: .milliseconds(300))
    #expect(store.saved == [5])
    #expect(m.applied == 5)
    #expect(m.status == .saved)
}

@MainActor @Test func savedTurnsIdleAfterTwoSeconds() async {
    let store = Store(1)
    let (m, clock) = makeModel(store)
    await m.load()
    m.draft = 2
    await m.saveNow()
    #expect(m.status == .saved)
    await clock.advance(by: .milliseconds(1900))
    #expect(m.status == .saved)
    await clock.advance(by: .milliseconds(100))
    #expect(m.status == .idle)
}

@MainActor @Test func revertingAnEditCancelsTheSave() async {
    let store = Store(1)
    let (m, clock) = makeModel(store)
    await m.load()
    m.draft = 2
    m.scheduleSave()
    m.draft = 1
    m.scheduleSave()
    await clock.advance(by: .seconds(1))
    #expect(store.saved.isEmpty)
    #expect(m.status == .idle)
}

@MainActor @Test func saveErrorIsKeptUntilTheNextSuccess() async {
    let store = Store(1)
    let (m, _) = makeModel(store)
    await m.load()
    store.failSaves([APIError.http(status: 401, body: "")])
    m.draft = 2
    await m.saveNow()
    #expect(m.saveError == .unauthorized)
    #expect(m.status == .error("Unauthorized — check the token in Connection."))
    #expect(m.applied == 1)
    #expect(m.hasUnsavedChanges)

    await m.saveNow()
    #expect(m.saveError == nil)
    #expect(m.status == .saved)
    #expect(store.saved == [2])
}

@MainActor @Test func saveRetriesRateLimitsThenSucceeds() async {
    let store = Store(1)
    let (m, clock) = makeModel(store)
    await m.load()
    store.failSaves([throttled, throttled])
    m.draft = 2
    let save = Task { await m.saveNow() }
    await clock.settle()
    #expect(m.status == .saving)
    await clock.advance(by: .seconds(1))
    #expect(m.status == .saving)
    await clock.advance(by: .seconds(1))
    await save.value
    #expect(store.saved == [2])
    #expect(m.status == .saved)
}

@MainActor @Test func saveGivesUpAfterThreeRateLimits() async {
    let store = Store(1)
    let (m, clock) = makeModel(store)
    await m.load()
    store.failSaves([throttled, throttled, throttled])
    m.draft = 2
    let save = Task { await m.saveNow() }
    await clock.advance(by: .seconds(3))
    await save.value
    #expect(m.saveError == .rateLimited)
    #expect(store.saved.isEmpty)
}

@MainActor @Test func loadRetriesRateLimits() async {
    let store = Store(9)
    let (m, clock) = makeModel(store)
    store.failLoads([throttled, throttled])
    let load = Task { await m.load() }
    await clock.advance(by: .seconds(2))
    await load.value
    #expect(m.applied == 9)
    #expect(m.loadError == nil)
    #expect(store.loads == 3)
}

@MainActor @Test func loadFailureIsReported() async {
    let store = Store(9)
    let (m, _) = makeModel(store)
    store.failLoads([APIError.http(status: 404, body: "")])
    await m.load()
    #expect(m.loadError == .featureOff)
    #expect(!m.isLoaded)
}

@MainActor @Test func reloadDoesNotClobberUnsavedEdits() async {
    let store = Store(1)
    let (m, _) = makeModel(store)
    await m.load()
    m.draft = 4
    m.scheduleSave()
    await m.load()
    #expect(m.draft == 4)
    #expect(store.loads == 1)
}

@MainActor @Test func editsDuringASaveGetTheirOwnSave() async {
    let store = Store(1)
    let (m, clock) = makeModel(store)
    await m.load()
    store.failSaves([throttled])
    m.draft = 2
    let save = Task { await m.saveNow() }
    await clock.settle()
    m.draft = 3                                  // while the first save waits out a 429
    await clock.advance(by: .seconds(1))
    await save.value
    #expect(store.saved == [2])
    await clock.advance(by: .milliseconds(600))
    #expect(store.saved == [2, 3])
}

@MainActor @Test func onSavedRunsAfterASuccessfulSave() async {
    let store = Store(1)
    let (m, _) = makeModel(store)
    var seen: [Int] = []
    m.onSaved = { seen.append($0) }
    await m.load()
    m.draft = 5
    await m.saveNow()
    #expect(seen == [5])
}

@Test func aggregateStatusPrefersErrorsThenSaving() {
    #expect(AggregateSaveStatus.combine([]) == .idle)
    #expect(AggregateSaveStatus.combine([.idle, .saved]) == .saved)
    #expect(AggregateSaveStatus.combine([.saved, .saving]) == .saving)
    #expect(AggregateSaveStatus.combine([.saving, .error("x"), .saved]) == .failed)
    #expect(AggregateSaveStatus.saving.subtitle == "Saving…")
    #expect(AggregateSaveStatus.failed.subtitle == "Couldn't save — see below")
    #expect(AggregateSaveStatus.idle.subtitle == nil)
}

@MainActor @Test func envModelReadsAndWritesProducerEnv() async throws {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true,
                                            attributes: [.posixPermissions: 0o700])
    defer { try? FileManager.default.removeItem(at: dir) }
    let path = dir.appendingPathComponent("producer.env")
    try "# keep me\nEMBER_SOURCE=m4\nEMBER_SERVER_URL=http://h:3627\nEMBER_TOKEN=secret\n"
        .write(to: path, atomically: true, encoding: .utf8)

    let m = EnvConfigModel<ConnectionSettings>(
        envAt: path, initial: ConnectionSettings(source: "", serverURL: "", sourceColor: ""),
        read: { ConnectionSettings(reading: $0) },
        apply: { v, env in try v.applyTolerant(to: &env, token: nil) })
    await m.load()
    #expect(m.draft.source == "m4")
    m.draft.source = "m5"
    await m.saveNow()
    #expect(m.status == .saved)
    let text = try String(contentsOf: path, encoding: .utf8)
    #expect(text.contains("EMBER_SOURCE=m5"))
    #expect(text.contains("EMBER_TOKEN=secret"))
    #expect(text.contains("# keep me"))
}

@MainActor @Test func settingsModelsAggregateTheirStatus() async {
    let client = stubbedClient { req in (okResponse(req.url!, status: 404), Data()) }
    let s = SettingsModels(client: client, envPath: URL(fileURLWithPath: "/nonexistent/producer.env"))
    #expect(s.all.count == 8)
    #expect(s.aggregateStatus == .idle)
    await s.pomodoro.load()
    #expect(s.pomodoro.loadError == .featureOff)
    #expect(s.pomodoro.draft == SettingsModels.defaultPomoConfig)
}
