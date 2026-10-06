import Foundation

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

    public func mint(hwID: String, name: String) async throws -> MintedKnob {
        try await client.request("POST", "/v1/devices",
                                 body: ["kind": KnobDevice.knobKind, "hw_id": hwID, "name": name])
    }

    public func config(id: String) async throws -> KnobSettings {
        try await client.get("/v1/devices/\(Self.escape(id))/config")
    }

    public func updateConfig(id: String, patch: [String: JSONValue]) async throws {
        try await client.put("/v1/devices/\(Self.escape(id))/config", body: JSONValue.object(patch))
    }

    public func rename(id: String, name: String) async throws -> KnobDevice {
        try await client.request("PATCH", "/v1/devices/\(Self.escape(id))", body: ["name": name])
    }

    public func rotate(id: String) async throws {
        try await client.send("POST", "/v1/devices/\(Self.escape(id))/rotate")
    }

    public func forget(id: String) async throws {
        try await client.send("DELETE", "/v1/devices/\(Self.escape(id))")
    }

    public func coredumps(id: String) async throws -> [KnobCoredump] {
        try await client.get("/v1/devices/\(Self.escape(id))/coredumps")
    }

    public func coredump(id: String, dump: String) async throws -> Data {
        try await client.getData("/v1/devices/\(Self.escape(id))/coredumps/\(Self.escape(dump))")
    }

    public func deleteCoredump(id: String, dump: String) async throws {
        try await client.send("DELETE", "/v1/devices/\(Self.escape(id))/coredumps/\(Self.escape(dump))")
    }

    static func escape(_ id: String) -> String {
        id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/"))) ?? id
    }
}
