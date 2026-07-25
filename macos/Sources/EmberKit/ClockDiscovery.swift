import Foundation
import Network
import Observation

/// Browses the LAN for AWTRIX clocks from *this Mac* — the client-side twin of
/// the server's `GET /v1/device/discover`.
///
/// The matching rules mirror the server's `internal/discovery` exactly: browse
/// `_http._tcp`, then keep only the hosts whose `/api/stats` answers 200 with a
/// non-empty `uid`. The point of running it here is environment, not logic — a
/// server in a bridge-networked container never sees multicast, while the app
/// has full-stack mDNS, so this path finds the clock when the server's cannot.
///
/// Degrades silently when Local Network access is denied or nothing is found;
/// the server-side scan and manual entry both remain available.
@MainActor
@Observable
public final class ClockDiscovery {
    /// Why the discovered-clocks list might be empty, so the UI can stop showing
    /// an indefinite "Searching…" when the browse can't actually run.
    public enum Status: Equatable, Sendable {
        case searching     // browsing (or just started)
        case needsAccess   // browse is waiting — usually Local Network access is off
        case unavailable   // browse failed outright
    }

    public private(set) var clocks: [DiscoveredClock] = []
    public private(set) var status: Status = .searching
    private var browser: NWBrowser?
    private var pending: [ObjectIdentifier: NWConnection] = [:]
    /// Bonjour instances already claimed for resolution this browse.
    private var resolving: Set<String> = []
    /// Base URLs already probed, so a re-resolved endpoint isn't re-fetched.
    private var probed: Set<String> = []
    /// In-flight probes, so `stop()` can cancel them. Finished ones linger until
    /// the next stop — one handle per probed host, cleared with the scan.
    private var probes: Set<Task<Void, Never>> = []

    public init() {}

    public func start() {
        guard browser == nil else { return }
        status = .searching
        let params = NWParameters()
        params.includePeerToPeer = false
        // `_http._tcp` is what AWTRIX3 advertises — a plain HTTP service, shared
        // with every other web-serving box on the LAN. The /api/stats
        // fingerprint below is what narrows it down.
        let b = NWBrowser(for: .bonjour(type: "_http._tcp", domain: nil), using: params)
        b.stateUpdateHandler = { [weak self] state in
            Task { @MainActor [weak self] in
                switch state {
                case .ready:
                    self?.status = .searching
                // A browse that can't proceed parks in .waiting — on a fresh build
                // that's almost always missing Local Network access.
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
            let services = results.compactMap { result -> (key: String, endpoint: NWEndpoint)? in
                guard case let .service(name, type, domain, _) = result.endpoint else { return nil }
                return ("\(name).\(type).\(domain)", result.endpoint)
            }
            Task { @MainActor [weak self] in self?.resolve(services) }
        }
        b.start(queue: .main)
        browser = b
    }

    /// Tears down and restarts the browse — used by the "Find clock" button, e.g.
    /// after the user grants Local Network access or moves the clock.
    public func restart() {
        stop()
        start()
    }

    public func stop() {
        browser?.cancel()
        browser = nil
        for conn in pending.values { conn.cancel() }
        pending.removeAll()
        // A probe outlives the browse by up to its resource timeout; uncancelled
        // it would land after `clocks` was cleared and repopulate a dismissed list.
        for probe in probes { probe.cancel() }
        probes.removeAll()
        resolving.removeAll()
        probed.removeAll()
        clocks = []
    }

    /// Claims a browsed service for resolution, returning false when it is
    /// already being (or has already been) resolved by this browse.
    ///
    /// `NWBrowser` replays the *whole* result set on every change, and mDNS
    /// answers trickle in over the first seconds — so without this, a LAN with N
    /// `_http._tcp` instances opens ~N TCP connections per callback. `_http._tcp`
    /// is the busiest service type on a home network (printers, NAS, speakers,
    /// ESPHome, HomeKit bridges), so the fan-out is real. The base-URL guard in
    /// `fingerprint` can't cover this: that key only exists once the connection
    /// has already succeeded.
    ///
    /// A service that fails to resolve isn't retried within the scan; "Find
    /// clock" restarts the browse, which clears the claims.
    func claim(_ key: String) -> Bool { resolving.insert(key).inserted }

    /// Resolves each newly-seen browsed service to an address, then fingerprints it.
    private func resolve(_ services: [(key: String, endpoint: NWEndpoint)]) {
        for (_, endpoint) in services.filter({ claim($0.key) }) {
            let params = NWParameters.tcp
            // Mirror the server's baseURLFor, which prefers an IPv4 literal:
            // AWTRIX serves plain HTTP on v4 and an IPv6 link-local address is
            // unusable as a bare URL host.
            if let ip = params.defaultProtocolStack.internetProtocol as? NWProtocolIP.Options {
                ip.version = .v4
            }
            let conn = NWConnection(to: endpoint, using: params)
            let key = ObjectIdentifier(conn)
            pending[key] = conn
            conn.stateUpdateHandler = { [weak self] state in
                switch state {
                case .ready:
                    if let path = conn.currentPath, case let .hostPort(host, port) = path.remoteEndpoint {
                        let hostStr = EndpointFormat.host(host)
                        let p = Int(port.rawValue)
                        Task { @MainActor [weak self] in self?.fingerprint(host: hostStr, port: p) }
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

    /// Probes one resolved host and keeps it if it fingerprints as a clock.
    private func fingerprint(host: String, port: Int) {
        guard !host.isEmpty else { return }
        let base = Self.baseURL(host: host, port: port)
        guard probed.insert(base).inserted else { return }
        probes.insert(Task { @MainActor [weak self] in
            let found = await Self.probe(host: host, baseURL: base)
            guard !Task.isCancelled, let self, let found else { return }
            clocks = Self.merged(clocks, adding: found)
        })
    }

    /// Cancels and forgets a resolution connection once it has resolved or failed.
    private func finish(_ key: ObjectIdentifier) {
        pending[key]?.cancel()
        pending[key] = nil
    }

    // MARK: Matching rules (pure — mirrors internal/discovery)

    /// Builds the `http://host:port` base URL for a resolved service. The port is
    /// always explicit and an absent one falls back to 80, so a candidate found
    /// here is byte-identical to the same clock reported by the server.
    nonisolated static func baseURL(host: String, port: Int) -> String {
        "http://\(EndpointFormat.urlHost(host)):\(port == 0 ? 80 : port)"
    }

    /// The `/api/stats` fields the fingerprint needs. Both are optional: the
    /// decode must survive an unrelated HTTP server answering with some other
    /// JSON shape.
    nonisolated private struct StatsProbe: Decodable {
        let uid: String?
        let version: String?
    }

    /// Decodes an `/api/stats` response into a candidate, or nil when the host
    /// isn't an AWTRIX clock. A non-200, an undecodable body, and an empty `uid`
    /// all mean "not a clock" — the same three rejections the server makes.
    nonisolated static func candidate(host: String, baseURL: String, status: Int, body: Data) -> DiscoveredClock? {
        guard status == 200,
              let p = try? JSONDecoder().decode(StatsProbe.self, from: body),
              let uid = p.uid, !uid.isEmpty
        else { return nil }
        return DiscoveredClock(host: host, baseURL: baseURL, uid: uid, version: p.version ?? "")
    }

    /// GETs `<baseURL>/api/stats` and returns the candidate when it fingerprints
    /// as a clock. Any transport failure is just "not a clock" — this runs
    /// against every `_http._tcp` host on the LAN, so failures are the norm.
    nonisolated static func probe(host: String, baseURL: String,
                      session: URLSession = probeSession) async -> DiscoveredClock? {
        guard let url = URL(string: baseURL + "/api/stats") else { return nil }
        var req = URLRequest(url: url)
        req.timeoutInterval = 1.5   // matches the server's probe client timeout
        guard let (data, resp) = try? await session.data(for: req) else { return nil }
        return candidate(host: host, baseURL: baseURL,
                         status: (resp as? HTTPURLResponse)?.statusCode ?? 0, body: data)
    }

    /// Short-timeout session for the probe fan-out: every unrelated web server on
    /// the LAN gets one of these, so they must not hold the scan open.
    nonisolated private static let probeSession: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 1.5
        config.timeoutIntervalForResource = 3
        return URLSession(configuration: config)
    }()

    /// Adds a candidate, de-duped by device `uid` (one clock can resolve through
    /// several endpoints), keeping the list ordered by host so the UI doesn't
    /// reshuffle as probes land out of order.
    nonisolated static func merged(_ list: [DiscoveredClock], adding c: DiscoveredClock) -> [DiscoveredClock] {
        guard !list.contains(where: { $0.uid == c.uid }) else { return list }
        return (list + [c]).sorted { ($0.host, $0.baseURL) < ($1.host, $1.baseURL) }
    }
}
