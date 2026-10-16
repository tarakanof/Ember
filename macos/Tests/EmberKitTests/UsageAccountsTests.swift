import Testing
import Foundation
@testable import EmberKit

private let accountsJSON = """
{"accounts":[
 {"id":"claude:3f9a1c0b7d2e4a65","tool":"claude","plan":"max","name":"Work","default_name":"Max",
  "hidden":false,"order":0,"first_seen":"2026-10-10T08:00:00Z","updated_at":"2026-10-16T09:58:00Z","stale":false},
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

@MainActor @Test func modelLoadsAndPutsOnlyTheChange() async throws {
    let puts = LockedBox()
    let client = stubbedClient(token: "t") { req in
        if req.httpMethod == "PUT" {
            let body = req.httpBody ?? req.httpBodyStreamData() ?? Data()
            puts.add(String(decoding: body, as: UTF8.self))
        }
        return (okResponse(req.url!), Data(accountsJSON.utf8))
    }
    let model = UsageAccounts.model(client: client)
    await model.load()
    #expect(model.draft.rows.count == 3)
    model.draft.setHidden("codex", true)
    await model.saveNow()
    #expect(model.saveError == nil)
    #expect(puts.paths == [#"{"codex":{"hidden":true}}"#])
}
