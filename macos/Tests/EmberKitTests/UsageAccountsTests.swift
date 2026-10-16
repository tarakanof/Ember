import Testing
import Foundation
@testable import EmberKit

private let accountsJSON = """
{"accounts":[
 {"id":"claude:3f9a1c0b7d2e4a65","tool":"claude","plan":"max","name":"Work","default_name":"Max",
  "hidden":false,"order":0,"first_seen":"2026-10-10T08:00:00Z","updated_at":"2026-10-16T11:58:00+02:00","stale":false},
 {"id":"claude:0123456789abcdef","tool":"claude","plan":"max","name":"Max","default_name":"Max",
  "hidden":true,"order":null,"first_seen":"2026-10-11T08:00:00Z","updated_at":"2026-10-15T07:00:00Z","stale":true},
 {"id":"codex","tool":"codex","plan":null,"name":"Codex","default_name":"Codex",
  "hidden":false,"order":null,"first_seen":"2026-10-12T08:00:00Z","updated_at":null,"stale":true}
]}
"""

private func decoded() throws -> UsageAccounts {
    let d = JSONDecoder()
    d.dateDecodingStrategy = .iso8601
    return UsageAccounts(try d.decode(UsageAccountList.self, from: Data(accountsJSON.utf8)))
}

private func json(_ body: [String: UsageAccountPrefs?]) throws -> [String: Any] {
    let data = try JSONEncoder().encode(body)
    return try #require(try JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]) as? [String: Any])
}

@Test func usageAccountsDecodeIntoRowsInServerOrder() throws {
    let rows = try decoded().rows
    #expect(rows.map(\.id) == ["claude:3f9a1c0b7d2e4a65", "claude:0123456789abcdef", "codex"])
    #expect(rows[0].tool == "claude")
    #expect(rows[0].customName == "Work")
    #expect(rows[0].displayName == "Work")
    #expect(rows[0].order == 0)
    #expect(rows[0].updatedAt == ISO8601DateFormatter().date(from: "2026-10-16T09:58:00Z"))
    #expect(!rows[0].stale)
    #expect(rows[1].hidden)
    #expect(rows[1].stale)
    #expect(rows[2].updatedAt == nil)
    #expect(rows[2].plan == nil)
}

@Test func aRowWithoutItsOwnNameShowsTheDefault() throws {
    let rows = try decoded().rows
    #expect(rows[1].customName.isEmpty)
    #expect(rows[1].displayName == "Max")
    #expect(rows[2].displayName == "Codex")
}

@Test func planLabelsMatchTheServer() throws {
    let rows = try decoded().rows
    #expect(rows[0].planLabel == "Max")
    #expect(rows[2].planLabel == nil)
}

@Test func anUnchangedListSendsNothing() throws {
    let accounts = try decoded()
    #expect(accounts.changes(since: accounts).isEmpty)
}

@Test func renamingSendsTheRowsWholeSettings() throws {
    let before = try decoded()
    var after = before
    after.rename("claude:0123456789abcdef", to: "  Personal  ")
    #expect(after.rows[1].customName == "Personal")
    let body = try json(after.changes(since: before))
    #expect(body.count == 1)
    let prefs = try #require(body["claude:0123456789abcdef"] as? [String: Any])
    #expect(prefs["name"] as? String == "Personal")
    #expect(prefs["hidden"] as? Bool == true)
    #expect(prefs["order"] == nil)
}

@Test func renameClampsToTheServerLimit() throws {
    var accounts = try decoded()
    accounts.rename("codex", to: String(repeating: "x", count: 40))
    #expect(accounts.rows[2].customName.count == UsageAccountRow.maxNameLength)
}

@Test func anEmptyNameFallsBackToTheDefault() throws {
    let before = try decoded()
    var after = before
    after.rename("claude:3f9a1c0b7d2e4a65", to: "   ")
    #expect(after.rows[0].displayName == "Max")
    let prefs = try #require(try json(after.changes(since: before))["claude:3f9a1c0b7d2e4a65"] as? [String: Any])
    #expect(prefs["name"] == nil)
    #expect(prefs["order"] as? Int == 0)
}

@Test func typingTheDefaultNameClearsTheCustomOne() throws {
    var accounts = try decoded()
    accounts.rename("claude:3f9a1c0b7d2e4a65", to: "Max")
    #expect(accounts.rows[0].customName.isEmpty)
}

@Test func hidingSendsHidden() throws {
    let before = try decoded()
    var after = before
    after.setHidden("codex", true)
    let body = try json(after.changes(since: before))
    #expect(body.count == 1)
    let prefs = try #require(body["codex"] as? [String: Any])
    #expect(prefs["hidden"] as? Bool == true)
    #expect(prefs["name"] == nil)
}

@Test func settingsBackToDefaultsSendNull() throws {
    let before = try decoded()
    var after = before
    after.setHidden("claude:0123456789abcdef", false)
    let data = try JSONEncoder().encode(after.changes(since: before))
    #expect(String(decoding: data, as: UTF8.self) == #"{"claude:0123456789abcdef":null}"#)
}

@Test func reorderNumbersEveryRowAndSendsTheMovedOnes() throws {
    let before = try decoded()
    var after = before
    after.move("codex", to: "claude:3f9a1c0b7d2e4a65")
    #expect(after.rows.map(\.id) == ["codex", "claude:3f9a1c0b7d2e4a65", "claude:0123456789abcdef"])
    #expect(after.rows.map(\.order) == [0, 1, 2])
    let body = try json(after.changes(since: before))
    #expect(Set(body.keys) == ["codex", "claude:3f9a1c0b7d2e4a65", "claude:0123456789abcdef"])
    #expect((body["codex"] as? [String: Any])?["order"] as? Int == 0)
    let first = try #require(body["claude:3f9a1c0b7d2e4a65"] as? [String: Any])
    #expect(first["order"] as? Int == 1)
    #expect(first["name"] as? String == "Work")
    let second = try #require(body["claude:0123456789abcdef"] as? [String: Any])
    #expect(second["order"] as? Int == 2)
    #expect(second["hidden"] as? Bool == true)
}

@Test func moveByStepsStopsAtTheEnds() throws {
    var accounts = try decoded()
    accounts.move("claude:3f9a1c0b7d2e4a65", by: -1)
    #expect(accounts.rows[0].id == "claude:3f9a1c0b7d2e4a65")
    #expect(accounts.rows[1].order == nil)
    accounts.move("claude:3f9a1c0b7d2e4a65", by: 1)
    #expect(accounts.rows.map(\.id) == ["claude:0123456789abcdef", "claude:3f9a1c0b7d2e4a65", "codex"])
    #expect(accounts.rows.map(\.order) == [0, 1, 2])
}

@Test func aNonUTCOffsetDecodesToTheSameInstant() throws {
    #expect(try decoded().rows[0].updatedAt == ISO8601DateFormatter().date(from: "2026-10-16T09:58:00Z"))
}

@Test func theNameLimitCountsCodePointsLikeTheServer() throws {
    let accounts = try decoded()
    let family = "\u{1F468}\u{200D}\u{1F469}\u{200D}\u{1F467}"
    let name = accounts.rows[2].normalizedName("Personal \(family) work \(family)")
    #expect(name.unicodeScalars.count <= UsageAccountRow.maxNameLength)
    #expect(name == "Personal \(family) work")
}

@Test func controlCharactersAreStripped() throws {
    let accounts = try decoded()
    #expect(accounts.rows[2].normalizedName("Wo\trk\u{7}") == "Work")
}

private let renumberedJSON = """
{"accounts":[
 {"id":"claude:3f9a1c0b7d2e4a65","tool":"claude","plan":"max","name":"Work","default_name":"Max",
  "hidden":false,"order":0,"first_seen":"2026-10-10T08:00:00Z","updated_at":"2026-10-16T09:58:00Z","stale":false},
 {"id":"claude:0123456789abcdef","tool":"claude","plan":"max","name":"Max 2","default_name":"Max 2",
  "hidden":false,"order":null,"first_seen":"2026-10-11T08:00:00Z","updated_at":"2026-10-15T07:00:00Z","stale":true},
 {"id":"codex","tool":"codex","plan":null,"name":"Codex","default_name":"Codex",
  "hidden":true,"order":null,"first_seen":"2026-10-12T08:00:00Z","updated_at":null,"stale":true}
]}
"""

private final class ServerStub: @unchecked Sendable {
    private let lock = NSLock()
    private var _gets = 0
    private var _puts: [[String: Any]] = []
    var gets: Int { lock.withLock { _gets } }
    var puts: [[String: Any]] { lock.withLock { _puts } }

    func handle(_ req: URLRequest, get: String,
                put: @escaping ([String: Any]) -> (Int, String)) -> (HTTPURLResponse, Data) {
        if req.httpMethod == "PUT" {
            let data = req.httpBody ?? req.httpBodyStreamData() ?? Data()
            let body = (try? JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
            lock.withLock { _puts.append(body) }
            let (status, reply) = put(body)
            return (okResponse(req.url!, status: status), Data(reply.utf8))
        }
        lock.withLock { _gets += 1 }
        return (okResponse(req.url!), Data(get.utf8))
    }
}

@MainActor @Test func aSaveAdoptsThePutReplyWithoutAnotherGet() async throws {
    let server = ServerStub()
    let client = stubbedClient(token: "t") { server.handle($0, get: accountsJSON) { _ in (200, renumberedJSON) } }
    let model = UsageAccounts.model(client: client)
    await model.load()
    model.draft.setHidden("codex", true)
    model.draft.setHidden("claude:0123456789abcdef", false)
    await model.saveNow()
    #expect(model.saveError == nil)
    #expect(server.gets == 1)
    #expect(server.puts.count == 1)
    #expect(server.puts[0]["claude:0123456789abcdef"] is NSNull)
    #expect((server.puts[0]["codex"] as? [String: Any])?["hidden"] as? Bool == true)
    #expect(model.draft.rows[1].defaultName == "Max 2")
    #expect(!model.hasUnsavedChanges)
}

@MainActor @Test func aReorderPutsEveryRowsWholeSettings() async throws {
    let server = ServerStub()
    let client = stubbedClient(token: "t") { server.handle($0, get: accountsJSON) { _ in (200, accountsJSON) } }
    let model = UsageAccounts.model(client: client)
    await model.load()
    model.draft.move("codex", to: "claude:3f9a1c0b7d2e4a65")
    await model.saveNow()
    let body = try #require(server.puts.first)
    #expect(NSDictionary(dictionary: body).isEqual(to: [
        "codex": ["order": 0],
        "claude:3f9a1c0b7d2e4a65": ["name": "Work", "order": 1],
        "claude:0123456789abcdef": ["hidden": true, "order": 2],
    ]))
}

@MainActor @Test func aForbiddenSaveRevertsToTheServerAndIsNotResent() async throws {
    let server = ServerStub()
    let client = stubbedClient(token: "t") {
        server.handle($0, get: accountsJSON) { _ in (403, #"{"error":"usage account settings need EMBER_TOKEN"}"#) }
    }
    let model = UsageAccounts.model(client: client)
    await model.load()
    let loaded = model.draft
    model.draft.move("codex", to: "claude:3f9a1c0b7d2e4a65")
    await model.saveNow()
    #expect(model.saveError != nil)
    #expect(model.draft == loaded)
    #expect(!model.hasUnsavedChanges)
    await model.load()
    #expect(server.puts.count == 1)
}

@MainActor @Test func aRejectedSaveRevertsAndIsNotResent() async throws {
    let server = ServerStub()
    let client = stubbedClient(token: "t") {
        server.handle($0, get: accountsJSON) { _ in (400, #"{"error":"bad name"}"#) }
    }
    let model = UsageAccounts.model(client: client)
    await model.load()
    let loaded = model.draft
    model.draft.rename("codex", to: "Laptop")
    await model.saveNow()
    #expect(model.saveError != nil)
    #expect(model.draft == loaded)
    await model.load()
    #expect(server.puts.count == 1)
}

@MainActor @Test func anAccountThatLeftTheServerIsDroppedFromTheRetry() async throws {
    let server = ServerStub()
    let gone = try JSONSerialization.jsonObject(with: Data(accountsJSON.utf8)) as! [String: Any]
    var accounts = gone["accounts"] as! [[String: Any]]
    accounts.removeLast()
    let withoutCodex = String(decoding: try JSONSerialization.data(withJSONObject: ["accounts": accounts]), as: UTF8.self)
    let first = LockedBox()
    let client = stubbedClient(token: "t") { req in
        let get = first.paths.isEmpty ? accountsJSON : withoutCodex
        first.add("get")
        return server.handle(req, get: get) { body in
            body["codex"] != nil ? (400, #"{"error":"unknown usage account \"codex\""}"#) : (200, withoutCodex)
        }
    }
    let model = UsageAccounts.model(client: client)
    await model.load()
    model.draft.move("claude:0123456789abcdef", to: "claude:3f9a1c0b7d2e4a65")
    await model.saveNow()
    #expect(model.saveError == nil)
    #expect(server.puts.count == 2)
    #expect(server.puts[1]["codex"] == nil)
    #expect(model.draft.rows.map(\.id) == ["claude:3f9a1c0b7d2e4a65", "claude:0123456789abcdef"])
}

@MainActor @Test func settingsModelsLoadTheAccounts() async {
    let server = ServerStub()
    let client = stubbedClient(token: "t") { req in
        req.url?.path == UsageAccounts.path
            ? server.handle(req, get: accountsJSON) { _ in (200, accountsJSON) }
            : (okResponse(req.url!, status: 404), Data())
    }
    let s = SettingsModels(client: client, envStore: EnvFileStore(path: URL(fileURLWithPath: "/nonexistent/producer.env")))
    await s.loadAll()
    #expect(s.usageAccounts.draft.rows.count == 3)
}
