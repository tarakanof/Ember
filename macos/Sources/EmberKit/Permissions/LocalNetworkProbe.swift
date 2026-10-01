import Foundation
import Network

/// Whether this app has Local Network access, as far as a probe can tell.
public enum LocalNetworkStatus: Equatable, Sendable {
    case granted, denied, unknown
}

/// Probes Local Network access with an explicit, short Bonjour browse for
/// `_ember._tcp`, plus a request to the configured server when that's a LAN
/// host.
public enum LocalNetworkProbe {
    /// What the browse reported before `browseTimeout`.
    public enum BrowseOutcome: Equatable, Sendable {
        /// Browsing (DNS-SD answered): access is on.
        case ready
        /// DNS-SD `NoAuth`/`PolicyDenied`: access is off.
        case denied
        /// Failed or waited for another reason, or never settled.
        case inconclusive
    }

    /// What `GET /healthz` on the configured server got back.
    public enum ServerOutcome: Equatable, Sendable {
        /// Any HTTP answer: the LAN is open to this app.
        case reachable
        /// `APIError.localNetworkDenied`.
        case denied
        /// No answer for another reason (server down, timeout).
        case unreachable
        /// No server configured, or it isn't on the LAN, so it says nothing.
        case notApplicable
    }

    public static let browseTimeout: Duration = .seconds(3)

    /// A denial from either side wins: each only reports one when macOS
    /// said so.
    public static func combine(browse: BrowseOutcome, server: ServerOutcome) -> LocalNetworkStatus {
        if browse == .denied || server == .denied { return .denied }
        if browse == .ready || server == .reachable { return .granted }
        return .unknown
    }

    /// Runs the browse and the server check together.
    @MainActor
    public static func run(client: APIClient) async -> LocalNetworkStatus {
        async let browse = self.browse()
        async let server = self.server(client)
        return combine(browse: await browse, server: await server)
    }

    /// Checks the server only when it's a LAN host; any HTTP status counts
    /// as reached.
    public static func server(_ client: APIClient) async -> ServerOutcome {
        guard let host = client.baseURL?.host(), LocalNetworkDenial.isLANHost(host) else { return .notApplicable }
        do {
            try await client.send("GET", "/healthz")
            return .reachable
        } catch {
            return serverOutcome(for: error)
        }
    }

    static func serverOutcome(for error: Error) -> ServerOutcome {
        switch error as? APIError {
        case .localNetworkDenied?: return .denied
        case .http?, .rateLimited?, .clockTimedOut?, .decoding?: return .reachable
        case .notConfigured?: return .notApplicable
        case .transport?, .timedOut?, nil: return .unreachable
        }
    }

    static func outcome(for state: NWBrowser.State) -> BrowseOutcome? {
        switch state {
        case .ready: return .ready
        case .failed(let e): return LocalNetworkDenial.isDenied(e) ? .denied : .inconclusive
        case .waiting(let e): return LocalNetworkDenial.isDenied(e) ? .denied : nil
        default: return nil
        }
    }

    /// A one-off `_ember._tcp` browse, ended by a result (on), a refusal
    /// (off) or `browseTimeout` (on if the browse got to ready, else
    /// inconclusive).
    @MainActor
    public static func browse(timeout: Duration = browseTimeout) async -> BrowseOutcome {
        let params = NWParameters()
        params.includePeerToPeer = false
        let browser = NWBrowser(for: .bonjour(type: "_ember._tcp", domain: nil), using: params)
        let verdict = await withCheckedContinuation { (cont: CheckedContinuation<BrowseOutcome, Never>) in
            let once = Once(cont)
            browser.stateUpdateHandler = { state in
                MainActor.assumeIsolated {
                    switch outcome(for: state) {
                    case .ready?: once.sawReady = true
                    case let v?: once.finish(v)
                    case nil: break
                    }
                }
            }
            browser.browseResultsChangedHandler = { results, _ in
                MainActor.assumeIsolated { if !results.isEmpty { once.finish(.ready) } }
            }
            browser.start(queue: .main)
            Task { @MainActor in
                try? await Task.sleep(for: timeout)
                once.finish(once.sawReady ? .ready : .inconclusive)
            }
        }
        browser.stateUpdateHandler = nil
        browser.browseResultsChangedHandler = nil
        browser.cancel()
        return verdict
    }

    @MainActor
    private final class Once {
        private var cont: CheckedContinuation<BrowseOutcome, Never>?
        var sawReady = false
        init(_ cont: CheckedContinuation<BrowseOutcome, Never>) { self.cont = cont }
        func finish(_ v: BrowseOutcome) {
            cont?.resume(returning: v)
            cont = nil
        }
    }
}
