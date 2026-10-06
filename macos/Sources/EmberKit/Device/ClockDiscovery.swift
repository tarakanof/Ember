import Foundation
import Network
import Observation
import os

public struct ClockService: Equatable, Sendable {
    public var name: String
    public var host: String
    public var port: Int
}

public enum ClockBrowseState: Sendable {
    case ready, denied, failed
}

@MainActor
protocol ClockBrowsing: AnyObject {
    func start(onState: @escaping @MainActor (ClockBrowseState) -> Void,
               onResolved: @escaping @MainActor (ClockService) -> Void)
    func cancel()
}

@MainActor
@Observable
public final class ClockDiscovery {
    public enum Access: Equatable, Sendable {
        case ok
        case needsAccess
        case unavailable
    }

    public private(set) var clocks: [DiscoveredClock] = []
    public private(set) var isScanning = false
    public private(set) var access: Access = .ok

    nonisolated static let browseWindow: Duration = .seconds(5)
    nonisolated static let probeGrace: Duration = .seconds(2)
    nonisolated static let probeTimeout: TimeInterval = 1.5

    typealias Probe = @Sendable (_ name: String, _ baseURL: String) async -> DiscoveredClock?
    typealias Sleep = @Sendable (Duration) async throws -> Void

    @ObservationIgnored private let makeBrowser: @MainActor () -> any ClockBrowsing
    @ObservationIgnored private let probe: Probe
    @ObservationIgnored private let sleep: Sleep
    @ObservationIgnored private var browser: (any ClockBrowsing)?
    @ObservationIgnored private var probes: [Task<Void, Never>] = []
    @ObservationIgnored private var probed: Set<String> = []
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
        case .denied: access = .needsAccess
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

    nonisolated static func baseURL(host: String, port: Int) -> String? {
        guard IPv4Address(host) != nil, host.contains(".") else { return nil }
        return "http://\(host):\(port == 0 ? 80 : port)"
    }

    private struct DeviceProbe: Decodable {
        let uid: String?
        let version: String?
        let boardType: String?
    }

    nonisolated static func candidate(name: String, baseURL: String, status: Int, body: Data) -> DiscoveredClock? {
        guard (200..<300).contains(status),
              let d = try? JSONDecoder().decode(DeviceProbe.self, from: body),
              let uid = d.uid, !uid.isEmpty, d.boardType == "awtrixng"
        else { return nil }
        return DiscoveredClock(host: name, baseURL: baseURL, uid: uid, version: d.version ?? "")
    }

    nonisolated static func probe(name: String, baseURL: String,
                                  session: URLSession = probeSession) async -> DiscoveredClock? {
        guard let url = URL(string: baseURL + "/api/v1/device") else { return nil }
        var req = URLRequest(url: url)
        req.timeoutInterval = probeTimeout
        let data: Data, resp: URLResponse
        do {
            (data, resp) = try await session.data(for: req)
        } catch {
            let reason = error.localizedDescription
            BonjourClockBrowser.log.notice("clock probe failed base_url=\(baseURL, privacy: .public) error=\(reason, privacy: .public)")
            return nil
        }
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        let found = candidate(name: name, baseURL: baseURL, status: status, body: data)
        if found == nil {
            BonjourClockBrowser.log.notice("clock probe rejected base_url=\(baseURL, privacy: .public) status=\(status, privacy: .public) reason=not_awtrixng")
        }
        return found
    }

    nonisolated private static let probeSession: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = probeTimeout
        config.timeoutIntervalForResource = 3
        return URLSession(configuration: config)
    }()

    nonisolated static func merged(_ list: [DiscoveredClock], adding c: DiscoveredClock) -> [DiscoveredClock] {
        guard !list.contains(where: { $0.uid == c.uid }) else { return list }
        return (list + [c]).sorted { ($0.host, $0.baseURL) < ($1.host, $1.baseURL) }
    }

    public nonisolated static func serverLostClock(health: Loadable<ClockHealth>, settingsLoaded: Bool,
                                                   settingsError: FeedError?) -> Bool {
        if case .loaded(let h, _) = health {
            guard let device = h.device else { return true }
            if !device.reachable && !h.publish.lastOk { return true }
        }
        if !settingsLoaded, case .server = settingsError { return true }
        return false
    }
}

public struct ClockChoice: Identifiable, Equatable, Sendable {
    public enum Source: Equatable, Sendable {
        case server, mac, both
    }

    public var clock: DiscoveredClock
    public var source: Source
    public var id: String { clock.uid }

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

public enum ClockURL {
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

@MainActor
final class BonjourClockBrowser: ClockBrowsing {
    static let serviceType = "_awtrixng._tcp"

    private var browser: NWBrowser?
    private var connections: [ObjectIdentifier: NWConnection] = [:]
    private var claimed: Set<String> = []

    func start(onState: @escaping @MainActor (ClockBrowseState) -> Void,
               onResolved: @escaping @MainActor (ClockService) -> Void) {
        let params = NWParameters()
        params.includePeerToPeer = false
        let b = NWBrowser(for: .bonjour(type: Self.serviceType, domain: nil), using: params)
        b.stateUpdateHandler = { state in
            let mapped = Self.browseState(for: state)
            switch state {
            case .waiting(let e), .failed(let e):
                Self.log.notice("browse \(String(describing: state), privacy: .public) error=\(String(describing: e), privacy: .public)")
            default: break
            }
            guard let mapped else { return }
            MainActor.assumeIsolated { onState(mapped) }
        }
        b.browseResultsChangedHandler = { [weak self] results, _ in
            let endpoints = results.map(\.endpoint)
            MainActor.assumeIsolated { self?.resolve(endpoints, onState, onResolved) }
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

    private func resolve(_ endpoints: [NWEndpoint], _ onState: @escaping @MainActor (ClockBrowseState) -> Void,
                         _ onResolved: @escaping @MainActor (ClockService) -> Void) {
        guard browser != nil else { return }
        for endpoint in endpoints {
            guard case let .service(name, type, domain, _) = endpoint,
                  claimed.insert("\(name).\(type).\(domain)").inserted
            else { continue }
            let params = NWParameters.udp
            if let ip = params.defaultProtocolStack.internetProtocol as? NWProtocolIP.Options {
                ip.version = .v4
            }
            let conn = NWConnection(to: endpoint, using: params)
            let key = ObjectIdentifier(conn)
            connections[key] = conn
            let claim = "\(name).\(type).\(domain)"
            conn.stateUpdateHandler = { [weak self] state in
                MainActor.assumeIsolated {
                    let what = String(describing: state)
                    switch Self.step(for: state) {
                    case .resolved:
                        if case let .hostPort(.ipv4(addr), port)? = conn.currentPath?.remoteEndpoint {
                            let host = String("\(addr)".split(separator: "%").first ?? "")
                            onResolved(ClockService(name: name, host: host, port: Int(port.rawValue)))
                        } else {
                            let remote = String(describing: conn.currentPath?.remoteEndpoint)
                            Self.log.notice("clock resolve dropped name=\(name, privacy: .public) reason=not_ipv4 endpoint=\(remote, privacy: .public)")
                        }
                        self?.finish(key)
                    case .keepWaiting:
                        Self.log.notice("clock resolve waiting name=\(name, privacy: .public) state=\(what, privacy: .public)")
                    case .denied:
                        Self.log.notice("clock resolve denied name=\(name, privacy: .public) state=\(what, privacy: .public)")
                        onState(.denied)
                        self?.finish(key)
                    case .failed:
                        Self.log.notice("clock resolve failed name=\(name, privacy: .public) state=\(what, privacy: .public)")
                        self?.claimed.remove(claim)
                        self?.finish(key)
                    case .ignore:
                        break
                    }
                }
            }
            conn.start(queue: .main)
        }
    }

    enum ResolveStep: Equatable {
        case resolved
        case keepWaiting
        case denied
        case failed
        case ignore
    }

    nonisolated static func step(for state: NWConnection.State) -> ResolveStep {
        switch state {
        case .ready: return .resolved
        case .waiting(let e): return isPolicyDenied(e) ? .denied : .keepWaiting
        case .failed(let e): return isPolicyDenied(e) ? .denied : .failed
        default: return .ignore
        }
    }

    nonisolated static func browseState(for state: NWBrowser.State) -> ClockBrowseState? {
        switch state {
        case .ready: return .ready
        case .waiting(let e): return isPolicyDenied(e) ? .denied : nil
        case .failed(let e): return isPolicyDenied(e) ? .denied : .failed
        default: return nil
        }
    }

    nonisolated static func isPolicyDenied(_ e: NWError) -> Bool {
        guard case .dns = e else { return false }
        return LocalNetworkDenial.isDenied(e)
    }

    nonisolated static let log = Logger(subsystem: "com.ember.Ember", category: "discovery")

    private func finish(_ key: ObjectIdentifier) {
        guard let conn = connections.removeValue(forKey: key) else { return }
        conn.stateUpdateHandler = nil
        conn.cancel()
    }
}
