import Foundation

public enum APIError: Error, Equatable, Sendable {
    case notConfigured
    case http(status: Int, body: String)
    case transport(String)
    case decoding(String)

    public var isUnauthorized: Bool {
        if case .http(401, _) = self { return true }
        return false
    }
}

/// Thin URLSession wrapper: injects the bearer token, encodes/decodes JSON, and
/// maps non-2xx + transport + decode failures to APIError. Sendable so it can be
/// captured by the Poller's tasks.
public struct APIClient: Sendable {
    public let baseURL: URL?
    public let token: String?
    let session: URLSession

    public init(baseURL: URL?, token: String?, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.token = token
        self.session = session
    }

    private static func makeDecoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    @discardableResult
    private func perform(_ method: String, _ path: String,
                         query: [URLQueryItem], body: Data?) async throws -> Data {
        guard let baseURL else { throw APIError.notConfigured }
        // Match the Go client: trim a trailing slash off the base, then append the
        // absolute path. Preserves any base path prefix and avoids double slashes.
        var base = baseURL.absoluteString
        if base.hasSuffix("/") { base.removeLast() }
        guard var comps = URLComponents(string: base + path) else { throw APIError.notConfigured }
        if !query.isEmpty { comps.queryItems = query }
        guard let url = comps.url else { throw APIError.notConfigured }

        var req = URLRequest(url: url)
        req.httpMethod = method
        if let token, !token.isEmpty {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        if let body {
            req.httpBody = body
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let data: Data
        let resp: URLResponse
        do {
            (data, resp) = try await session.data(for: req)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        guard let http = resp as? HTTPURLResponse else {
            throw APIError.transport("non-HTTP response")
        }
        guard (200..<300).contains(http.statusCode) else {
            let snippet = String(data: data.prefix(512), encoding: .utf8) ?? ""
            throw APIError.http(status: http.statusCode, body: snippet)
        }
        return data
    }

    public func get<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        let data = try await perform("GET", path, query: query, body: nil)
        do { return try Self.makeDecoder().decode(T.self, from: data) }
        catch { throw APIError.decoding(String(describing: error)) }
    }

    /// POST/DELETE with no body (e.g. the pomodoro action endpoints).
    public func send(_ method: String, _ path: String) async throws {
        _ = try await perform(method, path, query: [], body: nil)
    }

    public func put<B: Encodable>(_ path: String, body: B) async throws {
        let data = try JSONEncoder().encode(body)
        _ = try await perform("PUT", path, query: [], body: data)
    }
}
