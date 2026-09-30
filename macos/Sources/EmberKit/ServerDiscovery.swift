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
    private var holds = HoldCount()

    public init() {}

    /// Browses until the calling task is cancelled: use it from a view's
    /// `.task`. A permanent Bonjour browse costs mDNS traffic and wakeups, so it
    /// runs only while something shows the results. Holds are refcounted
    /// because SwiftUI can start a reappearing view's task before the old
    /// task's cancellation has run its `defer`.
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
        // Callbacks hop to the main actor, so one queued just before stop()
        // can land after it; only the current browser's are applied, or a
        // stale status or server would show on the next open.
        b.stateUpdateHandler = { [weak self, weak b] state in
            Task { @MainActor [weak self, weak b] in
                guard let self, let b, self.browser === b else { return }
                switch state {
                case .ready:
                    self.status = .searching
                // Only a PolicyDenied browse means Local Network access is off;
                // any other wait is transient and the browse keeps going. If
                // results arrive anyway, the list is shown regardless of status.
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
                        // A resolution that stop() already reclaimed is stale.
                        Task { @MainActor [weak self] in
                            guard let self, self.pending[key] != nil else { return }
                            self.add(Found(id: name, name: name, host: hostStr, port: p))
                        }
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

    /// Counts overlapping holders; the first acquire and the last release are
    /// the edges that start and stop the browse.
    struct HoldCount {
        private(set) var count = 0
        /// Returns true when this is the first holder.
        mutating func acquire() -> Bool {
            count += 1
            return count == 1
        }
        /// Returns true when this was the last holder.
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
        // Keep the full IPv6 description INCLUDING any `%zone` — a link-local
        // address is unusable without its zone id (urlString encodes it).
        case .ipv6(let a): return "\(a)"
        @unknown default: return ""
        }
    }
}
