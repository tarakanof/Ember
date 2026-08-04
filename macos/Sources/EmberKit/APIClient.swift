import Foundation

public enum APIError: Error, Equatable, Sendable {
    case notConfigured
    case http(status: Int, body: String)
    /// The request was still outstanding when our own budget ran out. Kept apart
    /// from `transport` because it proves nothing about *who* was slow — the
    /// server may have been waiting on something else (see `DeviceFailure`).
    case timeout
    case transport(String)
    case decoding(String)

    public var isUnauthorized: Bool {
        if case .http(401, _) = self { return true }
        return false
    }
}

// Without this conformance, settings footers render the NSError bridge —
// "EmberKit.APIError error 0." — instead of what the server actually said.
extension APIError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .notConfigured:
            return "Server not configured — set the server URL in Connection settings."
        case .http(let status, let body):
            let detail = Self.serverErrorText(body)
            return detail.isEmpty ? "HTTP \(status)" : "HTTP \(status) — \(detail)"
        case .timeout:
            return "The request timed out."
        case .transport(let message):
            return message
        case .decoding(let message):
            return "Unexpected server response — \(message)"
        }
    }

    /// The server wraps errors as {"error":"…"}; show that field when present,
    /// else fall back to the (trimmed) raw body snippet.
    private static func serverErrorText(_ body: String) -> String {
        if let data = body.data(using: .utf8),
           let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let msg = obj["error"] as? String, !msg.isEmpty {
            return msg
        }
        return body.trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

/// Thin URLSession wrapper: injects the bearer token, encodes/decodes JSON, and
/// maps non-2xx + transport + decode failures to APIError. Sendable so it can be
/// captured by the Poller's tasks.
public struct APIClient: Sendable {
    public let baseURL: URL?
    public let token: String?
    let session: URLSession

    public init(baseURL: URL?, token: String?, session: URLSession? = nil) {
        self.baseURL = baseURL
        self.token = token
        self.session = session ?? Self.defaultSession
    }

    /// Dedicated session (not `URLSession.shared`) with short timeouts so a
    /// "Test Connection" against a wrong/vanished host fails fast (~5s) instead of
    /// hanging on the 60s system defaults — and the tray reflects a dropped server
    /// promptly. 5s matches `DeviceService.directScreen`'s precedent; the 10s
    /// resource cap bounds the whole request incl. retries.
    private static let defaultSession: URLSession = {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 5
        config.timeoutIntervalForResource = 10
        return URLSession(configuration: config)
    }()

    /// A copy of this client that talks to the `/v1/device/*` proxies, which are
    /// slower than every other route by design: the server gives its request to
    /// the clock an 8s budget (`deviceBaseClient`, cmd/ember/device_settings.go)
    /// before answering 502. Under the default 5s budget the app aborts first and
    /// never sees that 502 — so a blackholed clock IP (issue #56, the case this
    /// discovery feature exists for) looks like a dead *server* instead of a dead
    /// clock, and the UI steers the user away from the fix. Everything else keeps
    /// the 5s fail-fast so "Test Connection" and the tray stay responsive.
    public func forDeviceProxy() -> APIClient {
        APIClient(baseURL: baseURL, token: token, session: Self.deviceProxySession)
    }

    /// Request budget for the device proxies: comfortably above the server's own
    /// 8s clock budget, so its 502 wins the race. The resource cap bounds the
    /// whole exchange.
    private static let deviceProxySession: URLSession = {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 12
        config.timeoutIntervalForResource = 25
        return URLSession(configuration: config)
    }()

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
        } catch let error as URLError where error.code == .timedOut {
            throw APIError.timeout
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

    public func post<B: Encodable>(_ path: String, body: B) async throws {
        let data = try JSONEncoder().encode(body)
        _ = try await perform("POST", path, query: [], body: data)
    }
}
