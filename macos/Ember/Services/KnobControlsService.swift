import CoreGraphics
import Foundation
import Observation
import SystemConfiguration
import EmberKit

@MainActor
@Observable
final class KnobControlsService {
    private(set) var macName: String = ThisMac.name(source: "", computerName: KnobControlsService.computerName)
    private(set) var eventAccess: Bool = CGPreflightPostEventAccess()

    @ObservationIgnored private let listener = ControlsListener(executor: MacControlsExecutor())
    @ObservationIgnored private var client: APIClient
    @ObservationIgnored private let envStore: EnvFileStore

    init(client: APIClient, envStore: EnvFileStore) {
        self.client = client
        self.envStore = envStore
    }

    func start() {
        Task { await listen() }
    }

    func reconfigure(client: APIClient) {
        self.client = client
        Task { await listen() }
    }

    func refreshEventAccess() {
        eventAccess = CGPreflightPostEventAccess()
    }

    func requestEventAccess() {
        eventAccess = MacControlsExecutor.requestEventAccess()
    }

    private func listen() async {
        let source = ConnectionSettings(reading: await envStore.read()).source
        macName = ThisMac.name(source: source, computerName: Self.computerName)
        listener.start(source: ControlsClient(client: client), mac: macName)
    }

    private static var computerName: String {
        (SCDynamicStoreCopyComputerName(nil, nil) as String?) ?? "Mac"
    }
}
