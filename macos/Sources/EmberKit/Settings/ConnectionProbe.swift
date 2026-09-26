import Foundation

/// Settings › Connection's status row: can this Mac reach the server, and
/// does the token work?
public enum ConnectionProbe {
    public enum Result: Equatable, Sendable {
        /// Reached, token accepted. `version` from `GET /version` when known.
        case connected(version: String?)
        /// No (valid) server URL.
        case notConfigured
        case unauthorized
        /// Reached, but the limiter answered before the token was checked.
        case rateLimited
        case unreachable
        case serverError(status: Int)
    }

    /// Probes an always-mounted, auth-required route (`/v1/apps`; Pomodoro's
    /// config 404s when that feature is off), then reads the version.
    public static func run(_ client: APIClient) async -> Result {
        do {
            try await client.send("GET", "/v1/apps")
        } catch {
            return result(for: error)
        }
        let info: VersionInfo? = try? await client.get("/version")
        return .connected(version: info?.short)
    }

    static func result(for error: Error) -> Result {
        guard let e = error as? APIError else { return .unreachable }
        switch e {
        case .notConfigured: return .notConfigured
        case .http(401, _): return .unauthorized
        case .http(let status, _): return .serverError(status: status)
        // The limiter sits in front of auth: reachable, token untested.
        case .rateLimited: return .rateLimited
        case .transport: return .unreachable
        case .decoding: return .connected(version: nil)
        }
    }
}
