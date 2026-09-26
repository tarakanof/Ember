import Foundation
@testable import EmberKit

/// A URLProtocol that answers requests from a per-host handler registry, so tests
/// in different suites/files run in parallel without clobbering a shared handler.
final class StubURLProtocol: URLProtocol {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var handlers: [String: @Sendable (URLRequest) throws -> (HTTPURLResponse, Data)] = [:]

    static func register(host: String,
                         handler: @escaping @Sendable (URLRequest) throws -> (HTTPURLResponse, Data)) {
        lock.withLock { handlers[host] = handler }
    }

    override class func canInit(with request: URLRequest) -> Bool {
        guard let host = request.url?.host else { return false }
        return lock.withLock { handlers[host] != nil }
    }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let host = request.url?.host ?? ""
        guard let handler = Self.lock.withLock({ Self.handlers[host] }) else {
            client?.urlProtocol(self, didFailWithError: URLError(.badServerResponse)); return
        }
        // Run the handler off URLSession's shared loader thread, so one that
        // blocks (to hold a response in flight) stalls only its own request.
        // The client is still called back on this thread, per the URLProtocol
        // contract, via its run loop.
        nonisolated(unsafe) let proto = self
        nonisolated(unsafe) let loaderThread = Thread.current
        let request = self.request
        DispatchQueue.global().async {
            proto.result = Result { try handler(request) }
            proto.perform(#selector(StubURLProtocol.deliver), on: loaderThread, with: nil,
                          waitUntilDone: false, modes: [RunLoop.Mode.common.rawValue])
        }
    }
    override func stopLoading() {}

    /// Written on the handler queue, read in `deliver` on the loader thread;
    /// `perform(_:on:)` orders the two.
    nonisolated(unsafe) private var result: Result<(HTTPURLResponse, Data), Error>?

    @objc private func deliver() {
        switch result {
        case .success(let (resp, data)):
            client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        case .failure(let error):
            client?.urlProtocol(self, didFailWithError: error)
        case nil:
            break
        }
    }
}

/// Builds an APIClient routed through StubURLProtocol with a UNIQUE host, so the
/// handler can't collide with concurrently-running tests.
func stubbedClient(token: String? = nil,
                   handler: @escaping @Sendable (URLRequest) throws -> (HTTPURLResponse, Data)) -> APIClient {
    let host = "stub-\(UUID().uuidString.lowercased()).local"
    StubURLProtocol.register(host: host, handler: handler)
    let config = URLSessionConfiguration.ephemeral
    config.protocolClasses = [StubURLProtocol.self]
    let session = URLSession(configuration: config)
    return APIClient(baseURL: URL(string: "http://\(host)"), token: token, session: session)
}

func okResponse(_ url: URL, status: Int = 200) -> HTTPURLResponse {
    HTTPURLResponse(url: url, statusCode: status, httpVersion: nil, headerFields: nil)!
}

/// Records request paths (or query strings) from the stub's handler thread.
final class LockedBox: @unchecked Sendable {
    private let lock = NSLock()
    private var _paths: [String] = []
    func add(_ p: String) { lock.lock(); _paths.append(p); lock.unlock() }
    var paths: [String] { lock.lock(); defer { lock.unlock() }; return _paths }
}

extension URLRequest {
    /// URLProtocol receives httpBody for non-stream bodies; this helper keeps the
    /// test resilient if a body is delivered as a stream.
    func httpBodyStreamData() -> Data? {
        guard let stream = httpBodyStream else { return nil }
        stream.open(); defer { stream.close() }
        var data = Data(); var buf = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let n = stream.read(&buf, maxLength: buf.count)
            if n <= 0 { break }
            data.append(buf, count: n)
        }
        return data.isEmpty ? nil : data
    }
}
