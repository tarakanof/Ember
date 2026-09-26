import Foundation
import Network
import Observation

/// One `_awtrixng._tcp` instance resolved to an address.
public struct ClockService: Equatable, Sendable {
    /// The Bonjour instance name. NG names it after its hostname ("Awtrix"),
    /// which is what the server reports as `host`.
    public var name: String
    /// An IPv4 literal.
    public var host: String
    public var port: Int
}

/// What the browse under a scan is doing.
public enum ClockBrowseState: Sendable {
    case ready, waiting, failed
}

/// The mDNS browse under `ClockDiscovery`: finds `_awtrixng._tcp` instances and
/// resolves each to an address. A seam so the scan's lifecycle is testable
/// without a network.
@MainActor
protocol ClockBrowsing: AnyObject {
    func start(onState: @escaping @MainActor (ClockBrowseState) -> Void,
               onResolved: @escaping @MainActor (ClockService) -> Void)
    /// Stops browsing and resolving. Idempotent.
    func cancel()
}

/// Finds awtrix-ng clocks from this Mac: the app-side twin of the server's
/// `GET /v1/device/discover`, for when the server can't see multicast (a
/// bridge-networked container). Matching mirrors `internal/discovery`:
/// browse `_awtrixng._tcp`, then keep the hosts whose `GET /api/v1/device`
/// answers 2xx with a non-empty `uid` and `boardType == "awtrixng"`.
///
/// A scan is bounded and owned by its caller: `scan()` browses for
/// `browseWindow`, gives probes still out `probeGrace`, then stops on its
/// own, keeping what it found. Cancelling the task running it, or `stop()`,
/// tears everything down and clears the list. There is no FIND_AWTRIXNG
/// broadcast fallback: the Mac sits on the clock's LAN with working mDNS,
/// which is the case the server's fallback exists for.
@MainActor
@Observable
public final class ClockDiscovery {
    /// Whether this Mac can browse at all.
    public enum Access: Equatable, Sendable {
        case ok
        /// The browse is waiting: usually Local Network access is off (or
        /// there's no network).
        case needsAccess
        case unavailable
    }

    /// Clocks found by the current or last scan, ordered by host.
    public private(set) var clocks: [DiscoveredClock] = []
    public private(set) var isScanning = false
    public private(set) var access: Access = .ok

    /// Longer than the server's 3 s (`handleDeviceDiscover`): rows show as
    /// they land, and the clock's Wi-Fi drops enough packets that resolving
    /// it can take a few tries.
    nonisolated static let browseWindow: Duration = .seconds(5)
    /// The server's probe phase budget (`BrowseAWTRIX`).
    nonisolated static let probeGrace: Duration = .seconds(2)
    /// The server's per-probe timeout (`defaultProbeTimeout`).
    nonisolated static let probeTimeout: TimeInterval = 1.5

    typealias Probe = @Sendable (_ name: String, _ baseURL: String) async -> DiscoveredClock?
    typealias Sleep = @Sendable (Duration) async throws -> Void

    @ObservationIgnored private let makeBrowser: @MainActor () -> any ClockBrowsing
    @ObservationIgnored private let probe: Probe
    @ObservationIgnored private let sleep: Sleep
    @ObservationIgnored private var browser: (any ClockBrowsing)?
    @ObservationIgnored private var probes: [Task<Void, Never>] = []
    @ObservationIgnored private var probed: Set<String> = []
    /// Bumped by every stop; callbacks and probes from an older scan compare
    /// against it and drop themselves.
    @ObservationIgnored private var generation = 0

    public convenience init() {
        self.init(makeBrowser: { BonjourClockBrowser() },
                  probe: { name, base in await ClockDiscovery.probe(name: name, baseURL: base) },
                  sleep: { try await Task.sleep(for: $0) })
    }

    init(makeBrowser: @escaping @MainActor () -> any ClockBrowsing, probe: @escaping Probe,
         sleep: @escaping Sleep) {
        self.makeBrowser = makeBrowser
        self.probe = probe
        self.sleep = sleep
    }

    /// Runs one bounded scan, replacing any running one. Returns when the scan
    /// ends: its window ran out (results kept), `stop()` was called, or the
    /// calling task was cancelled (both clear the results).
    public func scan() async {
        stop()
        let gen = generation
        isScanning = true
        let b = makeBrowser()
        browser = b
        b.start(onState: { [weak self] state in self?.browseStateChanged(state, gen) },
                onResolved: { [weak self] service in self?.resolved(service, gen) })
        do {
            try await sleep(Self.browseWindow)
        } catch {
            if gen == generation { stop() }
            return
        }
        guard gen == generation else { return }
        browser?.cancel()
        browser = nil
        await drainProbes()
        guard gen == generation else { return }
        if Task.isCancelled {
            stop()
        } else {
            isScanning = false
        }
    }

    /// Cancels the scan and everything under it, and clears the list.
    public func stop() {
        generation += 1
        browser?.cancel()
        browser = nil
        for p in probes { p.cancel() }
        probes = []
        probed = []
        clocks = []
        access = .ok
        isScanning = false
    }

    /// Waits for the probes still out, cancelling them after `probeGrace`.
    private func drainProbes() async {
        let pending = probes
        guard !pending.isEmpty else { return }
        let sleep = self.sleep
        let deadline = Task {
            do { try await sleep(Self.probeGrace) } catch { return }
            for p in pending { p.cancel() }
        }
        await withTaskCancellationHandler {
            for p in pending { await p.value }
        } onCancel: {
            for p in pending { p.cancel() }
        }
        deadline.cancel()
    }

    private func browseStateChanged(_ state: ClockBrowseState, _ gen: Int) {
        guard gen == generation else { return }
        switch state {
        case .ready: access = .ok
        case .waiting: access = .needsAccess
        case .failed: access = .unavailable
        }
    }

    private func resolved(_ service: ClockService, _ gen: Int) {
        guard gen == generation,
              let base = Self.baseURL(host: service.host, port: service.port),
              probed.insert(base).inserted
        else { return }
        let probe = self.probe
        probes.append(Task { [weak self] in
            let found = await probe(service.name, base)
            guard let self, !Task.isCancelled, gen == self.generation, let found else { return }
            self.clocks = Self.merged(self.clocks, adding: found)
        })
    }

    // MARK: Matching rules (pure; mirror internal/discovery)

    /// `http://<ipv4>:<port>`, the port always explicit and 0 meaning 80, so a
    /// clock found here is byte-identical to the server's `baseURLFor`. nil for
    /// anything but an IPv4 literal: the resolve pins IPv4, and the server's
    /// IPv6 fallback isn't mirrored (a v6-only LAN is left to the server).
    nonisolated static func baseURL(host: String, port: Int) -> String? {
        guard IPv4Address(host) != nil, host.contains(".") else { return nil }
        return "http://\(host):\(port == 0 ? 80 : port)"
    }

    private struct DeviceProbe: Decodable {
        let uid: String?
        let version: String?
        let boardType: String?
    }

    /// A `GET /api/v1/device` reply as a candidate, or nil when the host isn't
    /// an awtrix-ng clock: a non-2xx, a body that isn't the device object, an
    /// empty `uid`, or a `boardType` other than "awtrixng".
    nonisolated static func candidate(name: String, baseURL: String, status: Int, body: Data) -> DiscoveredClock? {
        guard (200..<300).contains(status),
              let d = try? JSONDecoder().decode(DeviceProbe.self, from: body),
              let uid = d.uid, !uid.isEmpty, d.boardType == "awtrixng"
        else { return nil }
        return DiscoveredClock(host: name, baseURL: baseURL, uid: uid, version: d.version ?? "")
    }

    /// Fingerprints one host. Any failure means "not a clock".
    nonisolated static func probe(name: String, baseURL: String,
                                  session: URLSession = probeSession) async -> DiscoveredClock? {
        guard let url = URL(string: baseURL + "/api/v1/device") else { return nil }
        var req = URLRequest(url: url)
        req.timeoutInterval = probeTimeout
        guard let (data, resp) = try? await session.data(for: req) else { return nil }
        return candidate(name: name, baseURL: baseURL,
                         status: (resp as? HTTPURLResponse)?.statusCode ?? 0, body: data)
    }

    nonisolated private static let probeSession: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = probeTimeout
        config.timeoutIntervalForResource = 3
        return URLSession(configuration: config)
    }()

    /// Adds a candidate unless its `uid` is already listed (one clock can
    /// resolve through several records), keeping the list ordered by host and
    /// address so it doesn't reshuffle as probes land.
    nonisolated static func merged(_ list: [DiscoveredClock], adding c: DiscoveredClock) -> [DiscoveredClock] {
        guard !list.contains(where: { $0.uid == c.uid }) else { return list }
        return (list + [c]).sorted { ($0.host, $0.baseURL) < ($1.host, $1.baseURL) }
    }

    /// Whether to offer finding the clock from this Mac: the server's health
    /// says it can't reach the clock (or has none), or the proxied settings
    /// read failed on the server's side. An unreachable server or a rejected
    /// token isn't something discovery can fix.
    public nonisolated static func serverLostClock(health: ClockHealth?, settingsLoaded: Bool,
                                                   settingsError: FeedError?) -> Bool {
        if let health, health.device?.reachable != true { return true }
        if !settingsLoaded, case .server = settingsError { return true }
        return false
    }
}

/// A clock in the Discover sheet, and who found it.
public struct ClockChoice: Identifiable, Equatable, Sendable {
    public enum Source: Equatable, Sendable {
        case server, mac, both
    }

    public var clock: DiscoveredClock
    public var source: Source
    public var id: String { clock.uid }

    /// The server's and this Mac's results as one list, one row per `uid`,
    /// ordered by host. When both found a clock the server's address wins:
    /// the server has shown it can reach that one.
    public static func merge(server: [DiscoveredClock], mac: [DiscoveredClock]) -> [ClockChoice] {
        var out: [ClockChoice] = []
        for c in server where !out.contains(where: { $0.clock.uid == c.uid }) {
            out.append(ClockChoice(clock: c, source: .server))
        }
        for c in mac {
            if let i = out.firstIndex(where: { $0.clock.uid == c.uid }) {
                if out[i].source == .server { out[i].source = .both }
            } else {
                out.append(ClockChoice(clock: c, source: .mac))
            }
        }
        return out.sorted { ($0.clock.host, $0.clock.baseURL) < ($1.clock.host, $1.clock.baseURL) }
    }
}

/// Clock address comparison.
public enum ClockURL {
    /// Whether two clock addresses name the same endpoint: discovery always
    /// writes the port, a hand-entered address often omits it (http → 80).
    public static func same(_ a: String, _ b: String?) -> Bool {
        guard let b, let x = key(a), let y = key(b) else { return false }
        return x == y
    }

    private static func key(_ s: String) -> String? {
        guard let c = URLComponents(string: s.trimmingCharacters(in: .whitespacesAndNewlines)),
              let scheme = c.scheme?.lowercased(), let host = c.host?.lowercased(), !host.isEmpty
        else { return nil }
        let port = c.port ?? (scheme == "https" ? 443 : 80)
        return "\(scheme)://\(host):\(port)"
    }
}

/// The real browse: `NWBrowser` over `_awtrixng._tcp`, each instance resolved
/// by a UDP "connection" pinned to IPv4. UDP reaches `.ready` once the address
/// resolves, without a handshake with the clock.
@MainActor
final class BonjourClockBrowser: ClockBrowsing {
    static let serviceType = "_awtrixng._tcp"

    private var browser: NWBrowser?
    private var connections: [ObjectIdentifier: NWConnection] = [:]
    /// Instances already being (or done) resolving. NWBrowser replays the
    /// whole result set on every change.
    private var claimed: Set<String> = []

    func start(onState: @escaping @MainActor (ClockBrowseState) -> Void,
               onResolved: @escaping @MainActor (ClockService) -> Void) {
        let params = NWParameters()
        params.includePeerToPeer = false
        let b = NWBrowser(for: .bonjour(type: Self.serviceType, domain: nil), using: params)
        b.stateUpdateHandler = { state in
            let mapped: ClockBrowseState? = switch state {
            case .ready: .ready
            case .waiting: .waiting
            case .failed: .failed
            default: nil
            }
            guard let mapped else { return }
            MainActor.assumeIsolated { onState(mapped) }
        }
        b.browseResultsChangedHandler = { [weak self] results, _ in
            let endpoints = results.map(\.endpoint)
            MainActor.assumeIsolated { self?.resolve(endpoints, onResolved) }
        }
        b.start(queue: .main)
        browser = b
    }

    func cancel() {
        browser?.cancel()
        browser = nil
        for c in connections.values {
            c.stateUpdateHandler = nil
            c.cancel()
        }
        connections = [:]
        claimed = []
    }

    private func resolve(_ endpoints: [NWEndpoint], _ onResolved: @escaping @MainActor (ClockService) -> Void) {
        guard browser != nil else { return }
        for endpoint in endpoints {
            guard case let .service(name, type, domain, _) = endpoint,
                  claimed.insert("\(name).\(type).\(domain)").inserted
            else { continue }
            let params = NWParameters.udp
            // Mirrors the server's IPv4 preference; a link-local IPv6 address
            // isn't a URL the server can use.
            if let ip = params.defaultProtocolStack.internetProtocol as? NWProtocolIP.Options {
                ip.version = .v4
            }
            let conn = NWConnection(to: endpoint, using: params)
            let key = ObjectIdentifier(conn)
            connections[key] = conn
            conn.stateUpdateHandler = { [weak self] state in
                MainActor.assumeIsolated {
                    switch state {
                    case .ready:
                        if case let .hostPort(.ipv4(addr), port)? = conn.currentPath?.remoteEndpoint {
                            let host = String("\(addr)".split(separator: "%").first ?? "")
                            onResolved(ClockService(name: name, host: host, port: Int(port.rawValue)))
                        }
                        self?.finish(key)
                    // .waiting would retry forever; the next scan tries again.
                    case .failed, .waiting, .cancelled:
                        self?.finish(key)
                    default:
                        break
                    }
                }
            }
            conn.start(queue: .main)
        }
    }

    private func finish(_ key: ObjectIdentifier) {
        guard let conn = connections.removeValue(forKey: key) else { return }
        conn.stateUpdateHandler = nil
        conn.cancel()
    }
}
