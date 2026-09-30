import Foundation
import Network

public enum APIError: Error, Equatable, Sendable {
    case notConfigured
    case http(status: Int, body: String)
    /// 429: the server's per-IP limiter throttled this Mac. Its own case because
    /// it says nothing about the clock, the token, or the server's health — and
    /// a caller that lumps it in with the rest reports the wrong cause.
    case rateLimited(retryAfter: Duration)
    case transport(String)
    /// No answer came within the request's budget (`URLError.timedOut`). The
    /// server may be up and waiting on the clock, so this is not reported as
    /// "unreachable", which is for requests that never left.
    case timedOut
    /// macOS Local Network privacy refused the connection to a LAN server
    /// (`LocalNetworkDenial`): the server may be fine.
    case localNetworkDenied
    /// 504 with code `clock_timeout`: the server answered, but its clock
    /// work ran out of the server's budget (`clockWriteBudget`, or
    /// `clockReadBudget` for the settings read). Carries what the server knows
    /// about the write; nil for a read, which has none.
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

    /// How long the server asked us to wait; nil for every other error.
    public var retryAfter: Duration? {
        if case .rateLimited(let d) = self { return d }
        return nil
    }
}

/// What a `clock_timeout` 504 says about the clock write: the server's
/// `write` field (`writeOutcome` in cmd/ember/clock_access.go).
public enum ClockWriteOutcome: String, Equatable, Sendable {
    /// The budget ran out before the write went out: nothing changed.
    case notSent = "not_sent"
    /// The write went out unanswered: it may or may not have landed.
    case unknown
    /// The write landed; the work after it didn't finish.
    case applied
}

extension APIError {
    /// The `clockTimedOut` a 504 body reports, or nil when it isn't a
    /// `clock_timeout`. No `write` field means a read ran out
    /// (`clockReadBudget`), so the outcome is nil; an unrecognised one reads
    /// as `.unknown`.
    static func clockTimeout(status: Int, body: Data) -> APIError? {
        guard status == 504,
              let obj = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              obj["code"] as? String == "clock_timeout" else { return nil }
        guard let write = obj["write"] as? String else { return .clockTimedOut(nil) }
        return .clockTimedOut(ClockWriteOutcome(rawValue: write) ?? .unknown)
    }
}

/// Thrown by `APIClient.postIdempotent` when the connection failed before any
/// bytes of the request reached the server, so it certainly had no effect.
public struct RequestNotSent: Error, LocalizedError, Equatable, Sendable {
    public let underlying: APIError
    public var errorDescription: String? { underlying.errorDescription }
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

/// How long a request may wait for the server, by what the server does before
/// it answers. Each budget sits above the server's own for that work
/// (`cmd/ember/clock_access.go`), so a slow clock normally reaches the app as
/// the server's 502, not as a timeout of ours.
public enum RequestBudget: Sendable, CaseIterable {
    /// Server-only work (`/healthz`, `/state`, settings): 5s, so a dead server
    /// shows promptly.
    case server
    /// One clock call through the server: `menuCallTimeout` is 8s. Discovery
    /// fits too (mDNS 3s, UDP fallback 3s, candidate probes 2s).
    case clock
    /// Work that chains clock calls or waits on a lock before one: a system
    /// read-merge-PUT queues behind another (two 8s calls each) and re-reads,
    /// a settings read or edit waits on the Pomodoro takeover snapshot and can
    /// re-write over a restore, a reminder fire waits up to 10s. The server
    /// bounds sensors/buttons/settings PUT at 25s (`clockWriteBudget`) and
    /// answers 504 when that runs out. Set above that and the server's 30s
    /// `WriteTimeout`, which doesn't stop a handler: one that still runs past
    /// it (a settings read) has its connection dropped, which `classify`
    /// reports as a timeout for this budget.
    case clockLong

    /// Longest wait for the response to start (`timeoutIntervalForRequest`).
    public var requestTimeout: TimeInterval {
        switch self {
        case .server: 5
        case .clock: 12
        case .clockLong: 35
        }
    }

    /// Longest whole request, retries included (`timeoutIntervalForResource`).
    public var resourceTimeout: TimeInterval {
        switch self {
        case .server: 10
        case .clock: 15
        case .clockLong: 40
        }
    }
}

/// Thin URLSession wrapper: injects the bearer token, encodes/decodes JSON, and
/// maps non-2xx + transport + decode failures to APIError. Sendable so it can be
/// captured by the Poller's tasks.
public struct APIClient: Sendable {
    public let baseURL: URL?
    public let token: String?
    /// The session for each budget: `session(for:)` unless a test injects one.
    let sessions: @Sendable (RequestBudget) -> URLSession
    /// This Mac's network path status, read when a request fails to tell a
    /// Local Network refusal from no network at all (`LocalNetworkDenial`).
    let pathStatus: @Sendable () -> NWPath.Status?

    public init(baseURL: URL?, token: String?, session: URLSession? = nil,
                pathStatus: (@Sendable () -> NWPath.Status?)? = nil) {
        let pick: @Sendable (RequestBudget) -> URLSession
        if let session { pick = { _ in session } } else { pick = { Self.session(for: $0) } }
        self.init(baseURL: baseURL, token: token, sessions: pick, pathStatus: pathStatus)
    }

    /// Picks a session per budget (tests record which budget a call used).
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

    /// The session a budget's requests run on: dedicated (not
    /// `URLSession.shared`, whose 60s defaults would leave "Test Connection"
    /// against a vanished host hanging), configured with the budget's timeouts.
    static func session(for budget: RequestBudget) -> URLSession {
        defaultSessions[budget]!
    }

    private static let defaultSessions: [RequestBudget: URLSession] = Dictionary(
        uniqueKeysWithValues: RequestBudget.allCases.map { budget in
            let config = URLSessionConfiguration.default
            config.timeoutIntervalForRequest = budget.requestTimeout
            config.timeoutIntervalForResource = budget.resourceTimeout
            // No cache headers from the server; the default disk cache only rewrote Cache.db every request.
            config.urlCache = nil
            config.requestCachePolicy = .reloadIgnoringLocalCacheData
            return (budget, URLSession(configuration: config))
        })

    /// URLError codes raised before the request left this Mac.
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
        return data
    }

    /// Maps a failed request to an APIError. A Local Network refusal wins over
    /// everything: macOS can surface one as a timeout, and the fix is a
    /// permission, not patience. Under `.clockLong` a dropped connection is a
    /// timeout too: the server took the request and closed it when its
    /// `WriteTimeout` passed, so the server was there, just slow. The budgeted
    /// handlers (the device PUTs, the settings GET) answer 504 instead, but
    /// other `.clockLong` requests can still drop.
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

    /// POST/DELETE with no body (e.g. the pomodoro action endpoints).
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

    /// POST carrying an `Idempotency-Key` so the server can drop a retry, with a
    /// timeout long enough to hear the server's answer instead of guessing.
    /// Throws `RequestNotSent` when the connection failed before sending.
    public func postIdempotent<B: Encodable>(_ path: String, body: B, key: String) async throws {
        let data = try JSONEncoder().encode(body)
        _ = try await perform("POST", path, query: [], body: data,
                              headers: ["Idempotency-Key": key], budget: .clockLong, reportNotSent: true)
    }
}
