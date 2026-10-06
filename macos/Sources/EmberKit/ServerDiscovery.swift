import Foundation
import Network
import Observation

@MainActor
@Observable
public final class ServerDiscovery {
    public struct Found: Identifiable, Hashable, Sendable {
        public let id: String
        public let name: String
        public let host: String
        public let port: Int
        public var urlString: String { "http://\(Self.urlHost(host)):\(port)" }
        static func urlHost(_ host: String) -> String {
            guard host.contains(":") else { return host }
            return "[\(host.replacingOccurrences(of: "%", with: "%25"))]"
        }
        public init(id: String, name: String, host: String, port: Int) {
            self.id = id; self.name = name; self.host = host; self.port = port
        }
    }

    public enum Status: Equatable, Sendable {
        case searching
        case needsAccess
        case unavailable
    }

    public private(set) var servers: [Found] = []
    public private(set) var status: Status = .searching
    private var browser: NWBrowser?
    private var pending: [ObjectIdentifier: NWConnection] = [:]
    private var holds = HoldCount()

    public init() {}

    public func browse() async {
        if holds.acquire() { start() }
        defer { if holds.release() { stop() } }
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(3600))
        }
    }

    public func start() {
        guard browser == nil else { return }
        status = .searching
        let params = NWParameters()
        params.includePeerToPeer = false
        let b = NWBrowser(for: .bonjour(type: "_ember._tcp", domain: nil), using: params)
        b.stateUpdateHandler = { [weak self, weak b] state in
            Task { @MainActor [weak self, weak b] in
                guard let self, let b, self.browser === b else { return }
                switch state {
                case .ready:
                    self.status = .searching
                case .waiting(let e), .failed(let e):
                    if BonjourClockBrowser.isPolicyDenied(e) {
                        self.status = .needsAccess
                    } else if case .failed = state {
                        self.status = .unavailable
                    }
                default:
                    break
                }
            }
        }
        b.browseResultsChangedHandler = { [weak self, weak b] results, _ in
            let endpoints = results.compactMap { result -> (String, NWEndpoint)? in
                if case let .service(name, _, _, _) = result.endpoint { return (name, result.endpoint) }
                return nil
            }
            Task { @MainActor [weak self, weak b] in
                guard let self, let b, self.browser === b else { return }
                self.resolve(endpoints)
            }
        }
        b.start(queue: .main)
        browser = b
    }

    public func restart() {
        stop()
        start()
    }

    public func stop() {
        browser?.cancel()
        browser = nil
        for conn in pending.values { conn.cancel() }
        pending.removeAll()
        servers = []
        status = .searching
    }

    private func resolve(_ endpoints: [(String, NWEndpoint)]) {
        for (name, endpoint) in endpoints where !servers.contains(where: { $0.id == name }) {
            let conn = NWConnection(to: endpoint, using: .tcp)
            let key = ObjectIdentifier(conn)
            pending[key] = conn
            conn.stateUpdateHandler = { [weak self] state in
                switch state {
                case .ready:
                    if let path = conn.currentPath, case let .hostPort(host, port) = path.remoteEndpoint {
                        let hostStr = Self.hostString(host)
                        let p = Int(port.rawValue)
                        Task { @MainActor [weak self] in
                            guard let self, self.pending[key] != nil else { return }
                            self.add(Found(id: name, name: name, host: hostStr, port: p))
                        }
                    }
                    Task { @MainActor [weak self] in self?.finish(key) }
                case .failed, .cancelled, .waiting:
                    Task { @MainActor [weak self] in self?.finish(key) }
                default:
                    break
                }
            }
            conn.start(queue: .main)
        }
    }

    private func add(_ f: Found) {
        guard !f.host.isEmpty, !servers.contains(where: { $0.id == f.id }) else { return }
        servers.append(f)
    }

    private func finish(_ key: ObjectIdentifier) {
        pending[key]?.cancel()
        pending[key] = nil
    }

    struct HoldCount {
        private(set) var count = 0
        mutating func acquire() -> Bool {
            count += 1
            return count == 1
        }
        mutating func release() -> Bool {
            guard count > 0 else { return false }
            count -= 1
            return count == 0
        }
    }

    nonisolated static func hostString(_ host: NWEndpoint.Host) -> String {
        switch host {
        case .name(let n, _): return n
        case .ipv4(let a): return String("\(a)".split(separator: "%").first ?? "")
        case .ipv6(let a): return "\(a)"
        @unknown default: return ""
        }
    }
}
