import Foundation

/// The owner side of the server's device registry (`/v1/devices`), with the
/// app's `EMBER_TOKEN`.
public struct KnobService: Sendable, Equatable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public static func == (a: KnobService, b: KnobService) -> Bool {
        a.client.baseURL == b.client.baseURL && a.client.token == b.client.token
    }

    public var baseURL: URL? { client.baseURL }

    public func devices() async throws -> [KnobDevice] {
        let list: KnobDeviceList = try await client.get("/v1/devices")
        return list.devices
    }

    /// Mints a device token; the same `hwID` again re-provisions (new token,
    /// old one revoked, config kept).
    public func mint(hwID: String, name: String) async throws -> MintedKnob {
        try await client.request("POST", "/v1/devices",
                                 body: ["kind": KnobDevice.knobKind, "hw_id": hwID, "name": name])
    }

    public func config(id: String) async throws -> KnobSettings {
        try await client.get("/v1/devices/\(Self.escape(id))/config")
    }

    /// Merge PUT: only the given fields change.
    public func updateConfig(id: String, patch: [String: JSONValue]) async throws {
        try await client.put("/v1/devices/\(Self.escape(id))/config", body: JSONValue.object(patch))
    }

    public func rename(id: String, name: String) async throws -> KnobDevice {
        try await client.request("PATCH", "/v1/devices/\(Self.escape(id))", body: ["name": name])
    }

    /// Starts a rotation: the knob collects the new token on its next checkin.
    public func rotate(id: String) async throws {
        try await client.send("POST", "/v1/devices/\(Self.escape(id))/rotate")
    }

    /// Revokes the token and forgets the knob.
    public func forget(id: String) async throws {
        try await client.send("DELETE", "/v1/devices/\(Self.escape(id))")
    }

    static func escape(_ id: String) -> String {
        id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? id
    }
}
