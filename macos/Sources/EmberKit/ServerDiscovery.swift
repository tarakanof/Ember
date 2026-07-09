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
        public var urlString: String { "http://\(Self.urlHost(host)):\(port)" }
        /// Formats a host for a URL authority. IPv6 literals (recognised by a
        /// colon) must be bracketed, and a link-local zone id keeps its `%`
        /// separator percent-encoded as `%25` (RFC 6874) — e.g. `fe80::1%en0`
        /// becomes `[fe80::1%25en0]`. IPv4 literals and hostnames pass through.
        static func urlHost(_ host: String) -> String {
            guard host.contains(":") else { return host }
            return "[\(host.replacingOccurrences(of: "%", with: "%25"))]"
        }
        public init(id: String, name: String, host: String, port: Int) {
            self.id = id; self.name = name; self.host = host; self.port = port
        }
    }

    /// Why the discovered-servers list might be empty, so the UI can stop showing
    /// an indefinite "Searching…" when the browse can't actually run.
    public enum Status: Equatable, Sendable {
        case searching     // browsing (or just started)
        case needsAccess   // browse is waiting — usually Local Network access is off
        case unavailable   // browse failed outright
    }

    public private(set) var servers: [Found] = []
    public private(set) var status: Status = .searching
    private var browser: NWBrowser?
    private var pending: [ObjectIdentifier: NWConnection] = [:]

    public init() {}

    public func start() {
        guard browser == nil else { return }
        status = .searching
        let params = NWParameters()
        params.includePeerToPeer = false
        let b = NWBrowser(for: .bonjour(type: "_ember._tcp", domain: nil), using: params)
        b.stateUpdateHandler = { [weak self] state in
            Task { @MainActor [weak self] in
                switch state {
                case .ready:
                    self?.status = .searching
                // A browse that can't proceed parks in .waiting — on a fresh build
                // that's almost always missing Local Network access. Surface it so
                // the UI can prompt instead of spinning forever. If results arrive
                // anyway, the list is shown regardless of status.
                case .waiting:
                    self?.status = .needsAccess
                case .failed:
                    self?.status = .unavailable
                default:
                    break
                }
            }
        }
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

    /// Tears down and restarts the browse — used by the "Rescan" button, e.g. after
    /// the user grants Local Network access.
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
                        Task { @MainActor [weak self] in self?.add(Found(id: name, name: name, host: hostStr, port: p)) }
                    }
                    Task { @MainActor [weak self] in self?.finish(key) }
                // .waiting means the path is unsatisfied (port filtered/refused) and
                // NWConnection would otherwise retry forever — fail fast and reclaim it.
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

    /// Cancels and forgets a resolution connection once it has resolved or failed.
    private func finish(_ key: ObjectIdentifier) {
        pending[key]?.cancel()
        pending[key] = nil
    }

    nonisolated static func hostString(_ host: NWEndpoint.Host) -> String {
        switch host {
        case .name(let n, _): return n
        case .ipv4(let a): return String("\(a)".split(separator: "%").first ?? "")
        // Keep the full IPv6 description INCLUDING any `%zone` — a link-local
        // address is unusable without its zone id (urlString encodes it).
        case .ipv6(let a): return "\(a)"
        @unknown default: return ""
        }
    }
}
