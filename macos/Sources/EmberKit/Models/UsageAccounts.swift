import Foundation

public struct UsageAccount: Codable, Sendable, Equatable {
    public var id: String
    public var tool: String
    public var plan: String?
    public var name: String
    public var nameSet: Bool?
    public var defaultName: String
    public var hidden: Bool
    public var order: Int?
    public var firstSeen: Date
    public var updatedAt: Date?
    public var stale: Bool

    enum CodingKeys: String, CodingKey {
        case id, tool, plan, name, hidden, order, stale
        case nameSet = "name_set"
        case defaultName = "default_name"
        case firstSeen = "first_seen"
        case updatedAt = "updated_at"
    }
}

public struct UsageAccountList: Codable, Sendable, Equatable {
    public var accounts: [UsageAccount]
}

public struct UsageAccountPrefs: Encodable, Sendable, Equatable {
    public var name: String
    public var hidden: Bool
    public var order: Int?

    public var isDefault: Bool { name.isEmpty && !hidden && order == nil }

    enum CodingKeys: String, CodingKey { case name, hidden, order }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        if !name.isEmpty { try c.encode(name, forKey: .name) }
        if hidden { try c.encode(hidden, forKey: .hidden) }
        try c.encodeIfPresent(order, forKey: .order)
    }
}

public struct UsageAccountRow: Sendable, Equatable, Identifiable {
    public static let maxNameLength = 24

    public let id: String
    public let tool: String
    public let plan: String?
    public let defaultName: String
    public var customName: String
    public var hidden: Bool
    public var order: Int?
    public let updatedAt: Date?
    public let stale: Bool

    public init(_ account: UsageAccount) {
        id = account.id
        tool = account.tool
        plan = account.plan
        defaultName = account.defaultName
        let named = account.nameSet ?? (account.name != account.defaultName)
        customName = named ? account.name : ""
        hidden = account.hidden
        order = account.order
        updatedAt = account.updatedAt
        stale = account.stale
    }

    public func normalizedName(_ name: String) -> String {
        var scalars = String.UnicodeScalarView()
        scalars.append(contentsOf: name.unicodeScalars.filter { $0.properties.generalCategory != .control })
        var kept = ""
        var count = 0
        for character in String(scalars).trimmingCharacters(in: .whitespacesAndNewlines) {
            count += character.unicodeScalars.count
            if count > Self.maxNameLength { break }
            kept.append(character)
        }
        return kept.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    public var displayName: String { customName.isEmpty ? defaultName : customName }

    public var prefs: UsageAccountPrefs { UsageAccountPrefs(name: customName, hidden: hidden, order: order) }

    public var planLabel: String? {
        guard let plan, !plan.isEmpty else { return nil }
        if let known = Self.planLabels[plan] { return known }
        let spaced = plan.replacingOccurrences(of: "_", with: " ")
        return spaced.prefix(1).uppercased() + spaced.dropFirst()
    }

    private static let planLabels = [
        "max": "Max", "pro": "Pro", "team": "Team", "enterprise": "Enterprise", "plus": "Plus",
        "business": "Business", "free": "Free", "edu": "Edu", "go": "Go",
    ]
}

public struct UsageAccounts: Sendable, Equatable {
    public static let path = "/v1/usage/accounts"

    public var rows: [UsageAccountRow]

    public init(rows: [UsageAccountRow] = []) { self.rows = rows }

    public init(_ list: UsageAccountList) { rows = list.accounts.map(UsageAccountRow.init) }

    public mutating func rename(_ id: String, to name: String) {
        guard let i = rows.firstIndex(where: { $0.id == id }) else { return }
        rows[i].customName = rows[i].normalizedName(name)
    }

    public mutating func setHidden(_ id: String, _ hidden: Bool) {
        guard let i = rows.firstIndex(where: { $0.id == id }) else { return }
        rows[i].hidden = hidden
    }

    public mutating func move(_ id: String, to target: String) {
        guard id != target, let from = rows.firstIndex(where: { $0.id == id }),
              let to = rows.firstIndex(where: { $0.id == target }) else { return }
        let row = rows.remove(at: from)
        rows.insert(row, at: to)
        renumber()
    }

    public mutating func move(_ id: String, by delta: Int) {
        guard let from = rows.firstIndex(where: { $0.id == id }) else { return }
        let to = from + delta
        guard rows.indices.contains(to), to != from else { return }
        rows.swapAt(from, to)
        renumber()
    }

    private mutating func renumber() {
        for i in rows.indices { rows[i].order = i }
    }

    public func rebased(onto current: UsageAccounts, from sent: UsageAccounts) -> UsageAccounts {
        var out = current
        for row in rows {
            guard let i = out.rows.firstIndex(where: { $0.id == row.id }) else { continue }
            let old = sent.rows.first(where: { $0.id == row.id })
            if row.customName != old?.customName { out.rows[i].customName = row.customName }
            if row.hidden != old?.hidden { out.rows[i].hidden = row.hidden }
            if row.order != old?.order { out.rows[i].order = row.order }
        }
        let position = Dictionary(uniqueKeysWithValues: out.rows.enumerated().map { ($1.id, $0) })
        out.rows.sort { a, b in
            switch (a.order, b.order) {
            case let (x?, y?) where x != y: return x < y
            case (.some, nil): return true
            case (nil, .some): return false
            default: return position[a.id, default: 0] < position[b.id, default: 0]
            }
        }
        return out
    }

    public func changes(since previous: UsageAccounts?) -> [String: UsageAccountPrefs?] {
        var out: [String: UsageAccountPrefs?] = [:]
        for row in rows {
            let prefs = row.prefs
            if previous?.rows.first(where: { $0.id == row.id })?.prefs == prefs { continue }
            out.updateValue(prefs.isDefault ? nil : prefs, forKey: row.id)
        }
        return out
    }

    @MainActor public static func model(client: APIClient) -> ConfigModel<UsageAccounts> {
        ConfigModel<UsageAccounts>(
            initial: UsageAccounts(),
            load: { UsageAccounts(try await client.get(path) as UsageAccountList) },
            saveReturning: { value, previous in try await save(value, previous: previous, client: client) },
            debounce: .milliseconds(300), savedHold: .seconds(2), sleep: { try await Task.sleep(for: $0) })
    }

    private static func save(_ value: UsageAccounts, previous: UsageAccounts?,
                             client: APIClient) async throws -> UsageAccounts? {
        let listed = Set((previous ?? value).rows.map(\.id))
        let body = value.changes(since: previous).filter { listed.contains($0.key) }
        guard !body.isEmpty else { return nil }
        do {
            return try await put(body, client: client)
        } catch let error as APIError {
            guard case .http(let status, _) = error, status == 400 || status == 403 else { throw error }
            let fresh = try? await client.get(path) as UsageAccountList
            let current = fresh.map(UsageAccounts.init) ?? previous ?? UsageAccounts()
            var failure: Error = error
            if status == 400, fresh != nil {
                let live = Set(current.rows.map(\.id))
                let retry = body.filter { live.contains($0.key) }
                if !retry.isEmpty, retry.count < body.count {
                    do { return try await put(retry, client: client) } catch { failure = error }
                }
            }
            let rejected = (failure as? APIError).map { if case .http(400, _) = $0 { true } else { false } } ?? false
            throw SaveRecovered(current: current, cause: FeedError(failure)) { sent, draft in
                rejected ? draft.rebased(onto: current, from: sent) : current
            }
        }
    }

    private static func put(_ body: [String: UsageAccountPrefs?], client: APIClient) async throws -> UsageAccounts {
        UsageAccounts(try await client.request("PUT", path, body: body) as UsageAccountList)
    }
}
