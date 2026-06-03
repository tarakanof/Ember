import Foundation

public enum PomodoroAction: String, CaseIterable, Sendable {
    case start, pause, resume, stop, skip
}

/// Typed wrapper over the /v1/pomodoro/* endpoints. Mirrors the old Go
/// pomodoro_client.go surface.
public struct PomodoroService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func state() async throws -> PomoState { try await client.get("/v1/pomodoro/state") }
    public func stats() async throws -> PomoStats { try await client.get("/v1/pomodoro/stats") }
    public func getConfig() async throws -> PomoConfig { try await client.get("/v1/pomodoro/config") }
    public func putConfig(_ cfg: PomoConfig) async throws { try await client.put("/v1/pomodoro/config", body: cfg) }
    public func action(_ a: PomodoroAction) async throws { try await client.send("POST", "/v1/pomodoro/\(a.rawValue)") }
}
