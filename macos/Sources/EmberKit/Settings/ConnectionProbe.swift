import Foundation

public enum ConnectionProbe {
    public enum Result: Equatable, Sendable {
        case connected(version: String?)
        case notConfigured
        case unauthorized
        case rateLimited
        case unreachable
        case timedOut
        case localNetworkDenied
        case serverError(status: Int)
    }

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
        case .clockTimedOut: return .serverError(status: 504)
        case .rateLimited: return .rateLimited
        case .transport: return .unreachable
        case .timedOut: return .timedOut
        case .localNetworkDenied: return .localNetworkDenied
        case .decoding: return .connected(version: nil)
        }
    }
}
