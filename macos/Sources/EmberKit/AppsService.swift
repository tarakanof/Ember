import Foundation

/// Typed wrapper over the /v1/apps endpoints (per-tool clock visibility).
public struct AppsService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func list() async throws -> [AppToggle] {
        let res: AppsList = try await client.get("/v1/apps")
        return res.apps
    }

    public func set(_ name: String, enabled: Bool) async throws {
        try await client.put("/v1/apps", body: SetAppRequest(app: name, enabled: enabled))
    }
}

private struct SetAppRequest: Encodable { let app: String; let enabled: Bool }
