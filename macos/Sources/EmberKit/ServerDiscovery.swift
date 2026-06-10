import Foundation
import Network
import Observation

/// Browses the LAN for Ember servers advertising `_ember._tcp` and resolves each
/// to a usable host:port. Degrades silently when local-network access is denied
/// or nothing is found — the Connection tab keeps manual entry either way.
@MainActor
@Observable
public final class ServerDiscovery {
    public struct Found: Identifiable, Hashable, Sendable {
        public let id: String   // bonjour instance name
        public let name: String
        public let host: String
        public let port: Int
        public var urlString: String { "http://\(host):\(port)" }
        public init(id: String, name: String, host: String, port: Int) {
            self.id = id; self.name = name; self.host = host; self.port = port
        }
    }

    public private(set) var servers: [Found] = []
    private var browser: NWBrowser?

    public init() {}

    public func start() {
        guard browser == nil else { return }
        let params = NWParameters()
        params.includePeerToPeer = false
        let b = NWBrowser(for: .bonjour(type: "_ember._tcp", domain: nil), using: params)
        b.browseResultsChangedHandler = { [weak self] results, _ in
            let endpoints = results.compactMap { result -> (String, NWEndpoint)? in
                if case let .service(name, _, _, _) = result.endpoint { return (name, result.endpoint) }
                return nil
            }
            Task { @MainActor [weak self] in self?.resolve(endpoints) }
        }
        b.start(queue: .main)
        browser = b
    }

    public func stop() {
        browser?.cancel()
        browser = nil
        servers = []
    }

    private func resolve(_ endpoints: [(String, NWEndpoint)]) {
        for (name, endpoint) in endpoints where !servers.contains(where: { $0.id == name }) {
            let conn = NWConnection(to: endpoint, using: .tcp)
            conn.stateUpdateHandler = { [weak self] state in
                switch state {
                case .ready:
                    if let path = conn.currentPath, case let .hostPort(host, port) = path.remoteEndpoint {
                        let hostStr = Self.hostString(host)
                        let p = Int(port.rawValue)
                        Task { @MainActor [weak self] in
                            self?.add(Found(id: name, name: name, host: hostStr, port: p))
                        }
                    }
                    conn.cancel()
                case .failed, .cancelled:
                    conn.cancel()
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

    nonisolated static func hostString(_ host: NWEndpoint.Host) -> String {
        switch host {
        case .name(let n, _): return n
        case .ipv4(let a): return String("\(a)".split(separator: "%").first ?? "")
        case .ipv6(let a): return String("\(a)".split(separator: "%").first ?? "")
        @unknown default: return ""
        }
    }
}
