import Foundation
import Network

public enum LocalNetworkStatus: Equatable, Sendable {
    case granted, denied, unknown
}

public enum LocalNetworkProbe {
    public enum BrowseOutcome: Equatable, Sendable {
        case ready
        case denied
        case inconclusive
    }

    public enum ServerOutcome: Equatable, Sendable {
        case reachable
        case denied
        case unreachable
        case notApplicable
    }

    public static let browseTimeout: Duration = .seconds(3)

    public static func combine(browse: BrowseOutcome, server: ServerOutcome) -> LocalNetworkStatus {
        if browse == .denied || server == .denied { return .denied }
        if browse == .ready || server == .reachable { return .granted }
        return .unknown
    }

    @MainActor
    public static func run(client: APIClient) async -> LocalNetworkStatus {
        async let browse = self.browse()
        async let server = self.server(client)
        return combine(browse: await browse, server: await server)
    }

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
