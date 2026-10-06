import Foundation
import Network

public enum APIError: Error, Equatable, Sendable {
    case notConfigured
    case http(status: Int, body: String)
    case rateLimited(retryAfter: Duration)
    case transport(String)
    case timedOut
    case localNetworkDenied
    case clockTimedOut(ClockWriteOutcome?)
    case decoding(String)

    public var isUnauthorized: Bool {
        if case .http(401, _) = self { return true }
        return false
    }

    public var isRateLimited: Bool {
        if case .rateLimited = self { return true }
        return false
    }

    public var retryAfter: Duration? {
        if case .rateLimited(let d) = self { return d }
        return nil
    }
}

public enum ClockWriteOutcome: String, Equatable, Sendable {
    case notSent = "not_sent"
    case unknown
    case applied
}

extension APIError {
    static func clockTimeout(status: Int, body: Data) -> APIError? {
        guard status == 504,
              let obj = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              obj["code"] as? String == "clock_timeout" else { return nil }
        guard let write = obj["write"] as? String else { return .clockTimedOut(nil) }
        return .clockTimedOut(ClockWriteOutcome(rawValue: write) ?? .unknown)
    }
}

public struct RequestNotSent: Error, LocalizedError, Equatable, Sendable {
    public let underlying: APIError
    public var errorDescription: String? { underlying.errorDescription }
}

extension APIError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .notConfigured:
            return "Server not configured — set the server URL in Connection settings."
        case .http(let status, let body):
            let detail = Self.serverErrorText(body)
            return detail.isEmpty ? "HTTP \(status)" : "HTTP \(status) — \(detail)"
        case .rateLimited(let retryAfter):
            return "Server is rate-limiting this Mac — retrying in \(retryAfter.wholeSecondsRoundedUp)s."
        case .transport(let message):
            return message
        case .timedOut:
            return String(localized: FeedError.timedOut.message)
        case .localNetworkDenied:
            return "Local Network access is off for Ember — allow it in System Settings › Privacy & Security › Local Network."
        case .clockTimedOut(let outcome):
            return String(localized: FeedError.clockTimedOut(outcome).message)
        case .decoding(let message):
            return "Unexpected server response — \(message)"
        }
    }

    private static func serverErrorText(_ body: String) -> String {
        if let data = body.data(using: .utf8),
           let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
           let msg = obj["error"] as? String, !msg.isEmpty {
            return msg
        }
        return body.trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

public enum RequestBudget: Sendable, CaseIterable {
    case server
    case clock
    case clockLong

    public var requestTimeout: TimeInterval {
        switch self {
        case .server: 5
        case .clock: 12
        case .clockLong: 35
        }
    }

    public var resourceTimeout: TimeInterval {
        switch self {
        case .server: 10
        case .clock: 15
        case .clockLong: 40
        }
    }
}

public struct APIClient: Sendable {
    public let baseURL: URL?
    public let token: String?
    let sessions: @Sendable (RequestBudget) -> URLSession
    let pathStatus: @Sendable () -> NWPath.Status?

    public init(baseURL: URL?, token: String?, session: URLSession? = nil,
                pathStatus: (@Sendable () -> NWPath.Status?)? = nil) {
        let pick: @Sendable (RequestBudget) -> URLSession
        if let session { pick = { _ in session } } else { pick = { Self.session(for: $0) } }
        self.init(baseURL: baseURL, token: token, sessions: pick, pathStatus: pathStatus)
    }

    init(baseURL: URL?, token: String?, sessions: @escaping @Sendable (RequestBudget) -> URLSession,
         pathStatus: (@Sendable () -> NWPath.Status?)? = nil) {
        self.baseURL = baseURL
        self.token = token
        self.sessions = sessions
        if let pathStatus {
            self.pathStatus = pathStatus
        } else {
            let snapshot = NetworkPathSnapshot.shared
            self.pathStatus = { snapshot.status }
        }
    }

    static func session(for budget: RequestBudget) -> URLSession {
        defaultSessions[budget]!
    }

    private static let defaultSessions: [RequestBudget: URLSession] = Dictionary(
        uniqueKeysWithValues: RequestBudget.allCases.map { budget in
            let config = URLSessionConfiguration.default
            config.timeoutIntervalForRequest = budget.requestTimeout
            config.timeoutIntervalForResource = budget.resourceTimeout
            config.urlCache = nil
            config.requestCachePolicy = .reloadIgnoringLocalCacheData
            return (budget, URLSession(configuration: config))
        })

    private static let notSentCodes: Set<URLError.Code> = [
        .cannotFindHost, .cannotConnectToHost, .dnsLookupFailed, .notConnectedToInternet,
    ]

    private static func makeDecoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }

    @discardableResult
    private func perform(_ method: String, _ path: String,
                         query: [URLQueryItem], body: Data?,
                         headers: [String: String] = [:], budget: RequestBudget,
                         reportNotSent: Bool = false) async throws -> Data {
        try await performResponse(method, path, query: query, body: body, headers: headers, budget: budget,
                                  reportNotSent: reportNotSent).0
    }

    private func performResponse(_ method: String, _ path: String,
                                 query: [URLQueryItem], body: Data?,
                                 headers: [String: String] = [:], budget: RequestBudget,
                                 reportNotSent: Bool = false) async throws -> (Data, HTTPURLResponse) {
        guard let baseURL else { throw APIError.notConfigured }
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
        for (name, value) in headers { req.setValue(value, forHTTPHeaderField: name) }
        let data: Data
        let resp: URLResponse
        do {
            (data, resp) = try await sessions(budget).data(for: req)
        } catch {
            let apiError = Self.classify(error, budget: budget, host: url.host, pathStatus: pathStatus())
            let denied = apiError == .localNetworkDenied
            if reportNotSent, denied || (error as? URLError).map({ Self.notSentCodes.contains($0.code) }) == true {
                throw RequestNotSent(underlying: apiError)
            }
            throw apiError
        }
        guard let http = resp as? HTTPURLResponse else {
            throw APIError.transport("non-HTTP response")
        }
        guard (200..<300).contains(http.statusCode) else {
            if http.statusCode == 429 {
                throw APIError.rateLimited(retryAfter: RateLimitBackoff.retryAfter(
                    header: http.value(forHTTPHeaderField: "Retry-After")))
            }
            if let timeout = APIError.clockTimeout(status: http.statusCode, body: data) {
                throw timeout
            }
            let snippet = String(data: data.prefix(512), encoding: .utf8) ?? ""
            throw APIError.http(status: http.statusCode, body: snippet)
        }
        return (data, http)
    }

    static func classify(_ error: Error, budget: RequestBudget, host: String?,
                         pathStatus: NWPath.Status?) -> APIError {
        if LocalNetworkDenial.isDenied(error, host: host, pathStatus: pathStatus) {
            return .localNetworkDenied
        }
        switch (error as? URLError)?.code {
        case .timedOut?: return .timedOut
        case .networkConnectionLost? where budget == .clockLong: return .timedOut
        default: return .transport(error.localizedDescription)
        }
    }

    public func get<T: Decodable>(_ path: String, query: [URLQueryItem] = [],
                                  budget: RequestBudget = .server) async throws -> T {
        let data = try await perform("GET", path, query: query, body: nil, budget: budget)
        do { return try Self.makeDecoder().decode(T.self, from: data) }
        catch { throw APIError.decoding(String(describing: error)) }
    }

    public func getWithResponse<T: Decodable>(_ path: String, query: [URLQueryItem] = [],
                                              budget: RequestBudget = .server) async throws -> (T, HTTPURLResponse) {
        let (data, http) = try await performResponse("GET", path, query: query, body: nil, budget: budget)
        do { return (try Self.makeDecoder().decode(T.self, from: data), http) }
        catch { throw APIError.decoding(String(describing: error)) }
    }

    public func getData(_ path: String, query: [URLQueryItem] = [], budget: RequestBudget = .server) async throws -> Data {
        try await perform("GET", path, query: query, body: nil, budget: budget)
    }

    public func send(_ method: String, _ path: String, budget: RequestBudget = .server) async throws {
        _ = try await perform(method, path, query: [], body: nil, budget: budget)
    }

    public func put<B: Encodable>(_ path: String, body: B, budget: RequestBudget = .server) async throws {
        let data = try JSONEncoder().encode(body)
        _ = try await perform("PUT", path, query: [], body: data, budget: budget)
    }

    public func post<B: Encodable>(_ path: String, body: B, budget: RequestBudget = .server) async throws {
        let data = try JSONEncoder().encode(body)
        _ = try await perform("POST", path, query: [], body: data, budget: budget)
    }

    public func request<B: Encodable, T: Decodable>(_ method: String, _ path: String, body: B,
                                                    budget: RequestBudget = .server) async throws -> T {
        let sent = try JSONEncoder().encode(body)
        let data = try await perform(method, path, query: [], body: sent, budget: budget)
        do { return try Self.makeDecoder().decode(T.self, from: data) }
        catch { throw APIError.decoding(String(describing: error)) }
    }

    public func putData(_ path: String, query: [URLQueryItem], data: Data, contentType: String,
                        budget: RequestBudget = .server) async throws {
        _ = try await perform("PUT", path, query: query, body: data,
                              headers: ["Content-Type": contentType], budget: budget)
    }

    public func postIdempotent<B: Encodable>(_ path: String, body: B, key: String) async throws {
        let data = try JSONEncoder().encode(body)
        _ = try await perform("POST", path, query: [], body: data,
                              headers: ["Idempotency-Key": key], budget: .clockLong, reportNotSent: true)
    }
}
