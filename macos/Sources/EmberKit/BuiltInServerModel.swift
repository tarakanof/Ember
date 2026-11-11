import Foundation
import Observation

@MainActor
@Observable
public final class BuiltInServerModel {
    public private(set) var mode: ServerMode = .anotherServer
    public private(set) var status: BuiltInServerStatus = .off
    public private(set) var isLoaded = false
    public private(set) var isWorking = false
    public private(set) var error: String?
    public var paths: BuiltInServerPaths { service.paths }

    @ObservationIgnored public var onProducerEnvChanged: (@MainActor () async -> Void)?
    @ObservationIgnored private let service: BuiltInServerService
    @ObservationIgnored private var sawRunning = false

    public init(service: BuiltInServerService) {
        self.service = service
    }

    public func refresh() async {
        guard !isWorking else { return }
        let service = self.service
        let mode = service.mode()
        let status = await service.status()
        guard !isWorking else { return }
        self.mode = mode
        self.status = status
        isLoaded = true
        let running = if case .running = status { true } else { false }
        defer { sawRunning = running }
        guard mode == .thisMac, running, sawRunning else { return }
        do {
            if try await service.adoptProducers() { await onProducerEnvChanged?() }
        } catch {
            self.error = error.localizedDescription
        }
    }

    public func select(_ next: ServerMode) async {
        guard !isWorking, next != mode else { return }
        await work(mode: next, changesProducerEnv: true) { [service] progress in
            switch next {
            case .thisMac: try await service.turnOn(progress: progress)
            case .anotherServer: try await service.turnOff()
            }
        }
    }

    public func start() async {
        guard !isWorking, mode == .thisMac else { return }
        await work(mode: .thisMac, changesProducerEnv: false) { [service] progress in try await service.start(progress: progress) }
    }

    private func work(mode next: ServerMode, changesProducerEnv: Bool,
                      _ body: @escaping @Sendable (@escaping @Sendable (BuiltInServerStatus) async -> Void) async throws
                          -> BuiltInServerStatus) async {
        isWorking = true
        error = nil
        sawRunning = false
        mode = next
        let progress: @Sendable (BuiltInServerStatus) async -> Void = { [weak self] status in
            await MainActor.run { self?.status = status }
        }
        do {
            status = try await body(progress)
            mode = service.mode()
        } catch {
            self.error = error.localizedDescription
            mode = service.mode()
            status = await service.status()
        }
        isWorking = false
        if changesProducerEnv { await onProducerEnvChanged?() }
    }
}
