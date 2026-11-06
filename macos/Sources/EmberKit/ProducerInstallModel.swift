import Foundation
import Observation

@MainActor
@Observable
public final class ProducerInstallModel {
    public private(set) var snapshot: ProducerSnapshot?
    public private(set) var isWorking = false
    public private(set) var failure: String?
    public private(set) var lastRunSucceeded: Bool?

    @ObservationIgnored private let service: ProducerInstallService
    @ObservationIgnored private var seq = 0

    public init(service: ProducerInstallService) {
        self.service = service
    }

    public var isOn: Bool { snapshot?.toggle == .on }

    public func refresh() async {
        seq += 1
        let mine = seq
        let fresh = await service.snapshot()
        if mine == seq { snapshot = fresh }
    }

    public func setEnabled(_ on: Bool) async {
        await run { on ? await $0.installAll() : await $0.uninstallAll() }
    }

    public func setEnabled(_ agent: ProducerAgent, _ on: Bool) async {
        await run { await $0.setEnabled(agent, on) }
    }

    public func moveToEmber(_ agent: ProducerAgent) async {
        await run { await $0.moveToEmber(agent) }
    }

    public func configureClaudeHooks() async {
        await run { await $0.configureClaudeHooks() }
    }

    public func repair() async {
        await run { await $0.repairAll() }
    }

    private func run(_ operation: (ProducerInstallService) async -> [AgentOutcome]) async {
        guard !isWorking else { return }
        isWorking = true
        failure = nil
        let outcomes = await operation(service)
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
