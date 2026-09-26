import Foundation
import Observation

/// Observable cache over `ProducerInstallService` for the Agents pane:
/// detection and registration state are read off the main actor on demand
/// (pane appear, after an install) instead of on every render, and
/// install/uninstall run without blocking the UI.
@MainActor
@Observable
public final class ProducerInstallModel {
    /// nil until the first read lands, so the UI can say "Checking…" rather
    /// than a false "No agent detected".
    public private(set) var snapshot: ProducerSnapshot?
    public private(set) var isWorking = false
    /// "Codex failed: …" after an install/uninstall with a failed agent.
    public private(set) var failure: String?
    /// For the window's save status.
    public private(set) var lastRunSucceeded: Bool?

    @ObservationIgnored private let service: ProducerInstallService
    @ObservationIgnored private var seq = 0

    public init(service: ProducerInstallService) {
        self.service = service
    }

    /// Whether reporting is on for every detected agent.
    public var isOn: Bool { snapshot?.toggle == .on }

    /// Rereads the state. A read applies only if no newer one started, so a
    /// slow first read can't overwrite the one taken after an install.
    public func refresh() async {
        seq += 1
        let mine = seq
        let fresh = await service.snapshot()
        if mine == seq { snapshot = fresh }
    }

    /// Installs (on) or uninstalls (off) every detected agent.
    public func setEnabled(_ on: Bool) async {
        guard !isWorking else { return }
        isWorking = true
        failure = nil
        let outcomes = on ? await service.installAll() : await service.uninstallAll()
        await refresh()
        isWorking = false
        if let failed = outcomes.first(where: { $0.error != nil }), let error = failed.error {
            failure = "\(failed.agent.rawValue.capitalized): \(error.localizedDescription)"
            lastRunSucceeded = false
        } else {
            lastRunSucceeded = true
        }
    }
}
