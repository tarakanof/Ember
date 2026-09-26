import Foundation
import Observation

/// The server this Mac talks to: producer.env's URL and token as one
/// `APIClient`. Everything that calls the server derives its requests from
/// `client`; `reload()` is the one place a Connection save is picked up.
@MainActor
@Observable
public final class ServerConnection {
    /// The current client. A nil `baseURL` means unconfigured: requests throw
    /// `APIError.notConfigured`.
    public private(set) var client: APIClient
    /// The configured server, nil when producer.env has none.
    public var serverURL: URL? { client.baseURL }
    /// The clock routes (`/v1/device/*`) on the current server.
    public var device: DeviceService { DeviceService(client: client) }

    @ObservationIgnored private let read: () -> APIClient

    /// Reads producer.env at `envPath` (a missing file is an unconfigured client).
    public convenience init(envPath: URL) {
        self.init(read: {
            let text = (try? String(contentsOf: envPath, encoding: .utf8)) ?? ""
            return APIClient(producerEnv: EnvFile(parsing: text))
        })
    }

    /// Tests supply the "file".
    init(read: @escaping () -> APIClient) {
        self.read = read
        client = read()
    }

    /// Re-reads producer.env. The authoritative identity check: consumers'
    /// own `configure` guards are only defensive. Returns true when the URL or
    /// token changed; a
    /// save that keeps both (the source name or colour) keeps the client, so
    /// nothing downstream resets.
    @discardableResult
    public func reload() -> Bool {
        let next = read()
        guard ServerIdentity(next) != ServerIdentity(client) else { return false }
        client = next
        return true
    }
}
