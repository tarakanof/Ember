import Foundation
import Observation

@MainActor
@Observable
public final class ServerConnection {
    public private(set) var client: APIClient
    public var serverURL: URL? { client.baseURL }
    public var device: DeviceService { DeviceService(client: client) }

    @ObservationIgnored private let read: () -> APIClient

    public convenience init(envPath: URL) {
        self.init(read: {
            let text = (try? String(contentsOf: envPath, encoding: .utf8)) ?? ""
            return APIClient(producerEnv: EnvFile(parsing: text))
        })
    }

    init(read: @escaping () -> APIClient) {
        self.read = read
        client = read()
    }

    @discardableResult
    public func reload() -> Bool {
        let next = read()
        guard ServerIdentity(next) != ServerIdentity(client) else { return false }
        client = next
        return true
    }
}
