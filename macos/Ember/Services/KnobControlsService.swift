import CoreGraphics
import Foundation
import Observation
import OSLog
import SystemConfiguration
import EmberKit

@MainActor
@Observable
final class KnobControlsService {
    private static let log = Logger(subsystem: "com.ember.Ember", category: "controls")
    private static let refreshEvery: Duration = .seconds(30)

    private(set) var macName: String = ThisMac.name(source: "", computerName: KnobControlsService.computerName)
    private(set) var eventAccess: Bool = CGPreflightPostEventAccess()
    private(set) var target: String?
    private(set) var listening = false
    var mapping: ControlsMapping {
        didSet { if mapping != oldValue { mapping.save(to: .standard) } }
    }

    @ObservationIgnored private var listener: ControlsListener!
    @ObservationIgnored private var client: APIClient
    @ObservationIgnored private let envStore: EnvFileStore
    @ObservationIgnored private var refresher: Task<Void, Never>?

    init(client: APIClient, envStore: EnvFileStore) {
        self.client = client
        self.envStore = envStore
        mapping = ControlsMapping.load(from: .standard)
        listener = ControlsListener(executor: MacControlsExecutor(), mapping: { [weak self] in
            self?.mapping ?? ControlsMapping()
        }, isTarget: { [weak self] in
            guard let self else { return false }
            return ControlsGate.shouldListen(target: target, thisMac: macName, hasToken: !(client.token ?? "").isEmpty)
        })
    }

    func start() {
        refresher?.cancel()
        refresher = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(for: Self.refreshEvery)
            }
        }
    }

    func reconfigure(client: APIClient) {
        self.client = client
        target = nil
        apply()
        start()
    }

    func refresh() async {
        let source = ConnectionSettings(reading: await envStore.read()).source
        macName = ThisMac.name(source: source, computerName: Self.computerName)
        do {
            target = try await ControlsClient(client: client).config().target
        } catch {
            target = nil
        }
        apply()
    }

    func refreshEventAccess() {
        eventAccess = CGPreflightPostEventAccess()
    }

    func requestEventAccess() {
        eventAccess = MacControlsExecutor.requestEventAccess()
    }

    private func apply() {
        let should = ControlsGate.shouldListen(target: target, thisMac: macName, hasToken: !(client.token ?? "").isEmpty)
        guard should != listening else { return }
        listening = should
        if should {
            Self.log.notice("knob controls on: this Mac is the target")
            listener.start(source: ControlsClient(client: client), mac: macName)
        } else {
            Self.log.notice("knob controls off on this Mac")
            listener.stop()
        }
    }

    private static var computerName: String {
        (SCDynamicStoreCopyComputerName(nil, nil) as String?) ?? "Mac"
    }
}
