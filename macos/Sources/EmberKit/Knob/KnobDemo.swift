import Foundation
import Observation

public enum DeveloperTools {
    public static let storageKey = "settings.developerTools"
}

public struct KnobDemoScene: Codable, Equatable, Hashable, Sendable, Identifiable {
    public var name: String
    public var group: String
    public var title: String

    public var id: String { name }

    public init(name: String, group: String, title: String) {
        self.name = name; self.group = group; self.title = title
    }
}

public struct KnobDemoGroup: Equatable, Sendable, Identifiable {
    public var name: String
    public var scenes: [KnobDemoScene]

    public var id: String { name }

    public static func grouping(_ scenes: [KnobDemoScene]) -> [KnobDemoGroup] {
        var out: [KnobDemoGroup] = []
        for scene in scenes {
            if let i = out.firstIndex(where: { $0.name == scene.group }) {
                out[i].scenes.append(scene)
            } else {
                out.append(KnobDemoGroup(name: scene.group, scenes: [scene]))
            }
        }
        return out
    }
}

public struct KnobDemoStatus: Codable, Equatable, Sendable {
    public enum State: String, Codable, Sendable {
        case idle, pending, playing, unanswered
    }

    public var state: State
    public var scene: String
    public var id: String
    public var queueLeft: Int

    enum CodingKeys: String, CodingKey {
        case state, scene, id
        case queueLeft = "queue_left"
    }

    public init(state: State, scene: String = "", id: String = "", queueLeft: Int = 0) {
        self.state = state; self.scene = scene; self.id = id; self.queueLeft = queueLeft
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = (try? c.decode(State.self, forKey: .state)) ?? .idle
        scene = (try? c.decodeIfPresent(String.self, forKey: .scene)) ?? ""
        id = (try? c.decodeIfPresent(String.self, forKey: .id)) ?? ""
        queueLeft = (try? c.decodeIfPresent(Int.self, forKey: .queueLeft)) ?? 0
    }

    public static let idle = KnobDemoStatus(state: .idle)

    public var isActive: Bool { state == .pending || state == .playing }
}

public enum KnobDemoAvailability: Equatable, Sendable {
    case noKnob
    case needsFirmware
    case ready

    public init(knob: KnobDevice?) {
        guard let knob else { self = .noKnob; return }
        self = knob.supports(feature: KnobCaps.demo) ? .ready : .needsFirmware
    }
}

public enum KnobDemoLine: Equatable, Sendable {
    case idle
    case waiting(title: String)
    case playing(title: String, queueLeft: Int)
    case unanswered(title: String)
}

@MainActor
@Observable
public final class KnobDemoModel {
    public private(set) var groups: [KnobDemoGroup] = []
    public private(set) var scenesLoaded = false
    public private(set) var status = KnobDemoStatus.idle
    public private(set) var error: FeedError?
    public private(set) var catalogError: FeedError?
    public private(set) var busy = false
    public private(set) var followID = 0
    public var hold = false

    @ObservationIgnored private(set) var service: KnobService
    @ObservationIgnored public private(set) var deviceID: String?
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var statusEpoch = 0

    public static let pollInterval: Duration = .seconds(1)
    public static let maxRetryInterval: Duration = .seconds(16)

    public enum Refresh: Equatable, Sendable {
        case updated, failed, superseded
    }

    public init(service: KnobService, sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.service = service
        self.sleep = sleep
    }

    func configure(service next: KnobService, device: String?) {
        guard next != service || device != deviceID else { return }
        if next != service { groups = []; scenesLoaded = false }
        service = next
        deviceID = device
        generation += 1
        status = .idle
        error = nil
        catalogError = nil
        busy = false
    }

    public var line: KnobDemoLine {
        let title = title(of: status.scene)
        switch status.state {
        case .idle: return .idle
        case .pending: return .waiting(title: title)
        case .playing: return .playing(title: title, queueLeft: status.queueLeft)
        case .unanswered: return .unanswered(title: title)
        }
    }

    public func title(of name: String) -> String {
        for g in groups {
            if let s = g.scenes.first(where: { $0.name == name }) { return s.title }
        }
        return name
    }

    public func loadScenes() async {
        guard let id = deviceID else { return }
        let gen = generation
        do {
            let scenes = try await service.demoScenes(id: id)
            guard gen == generation else { return }
            groups = KnobDemoGroup.grouping(scenes)
            scenesLoaded = true
            catalogError = nil
        } catch {
            guard gen == generation, !(error is CancellationError) else { return }
            catalogError = FeedError(error)
        }
    }

    public func enter() async {
        if !scenesLoaded { await loadScenes() }
        await refreshStatus()
        await follow()
    }

    @discardableResult
    public func refreshStatus() async -> Refresh {
        guard let id = deviceID else { return .superseded }
        let gen = generation
        let epoch = statusEpoch
        do {
            let next = try await service.demoStatus(id: id)
            guard gen == generation, epoch == statusEpoch, !Task.isCancelled else { return .superseded }
            status = next
            error = nil
            return .updated
        } catch {
            guard gen == generation, epoch == statusEpoch, !Task.isCancelled, !(error is CancellationError) else {
                return .superseded
            }
            self.error = FeedError(error)
            return .failed
        }
    }

    public func play(_ scene: KnobDemoScene) async {
        await start(.init(scene: scene.name, scenes: nil, hold: hold))
    }

    public func playAll(_ group: KnobDemoGroup) async {
        await start(.init(scene: nil, scenes: group.scenes.map(\.name), hold: nil))
    }

    public func stop() async {
        guard let id = deviceID else { return }
        let gen = generation
        statusEpoch &+= 1
        let epoch = statusEpoch
        busy = true
        defer { if gen == generation { busy = false } }
        do {
            try await service.stopDemo(id: id)
            guard gen == generation, epoch == statusEpoch else { return }
            statusEpoch &+= 1
            status = .idle
            error = nil
        } catch {
            guard gen == generation, epoch == statusEpoch, !(error is CancellationError) else { return }
            statusEpoch &+= 1
            self.error = FeedError(error)
        }
    }

    public func follow() async {
        let gen = generation
        var wait = Self.pollInterval
        while status.isActive, gen == generation, !Task.isCancelled {
            do { try await sleep(wait) } catch { return }
            guard gen == generation, !Task.isCancelled else { return }
            switch await refreshStatus() {
            case .updated, .superseded: wait = Self.pollInterval
            case .failed: wait = min(wait * 2, Self.maxRetryInterval)
            }
        }
    }

    private func start(_ request: KnobDemoRequest) async {
        guard let id = deviceID else { return }
        let gen = generation
        statusEpoch &+= 1
        let epoch = statusEpoch
        busy = true
        defer { if gen == generation { busy = false } }
        do {
            let next = try await service.startDemo(id: id, request)
            guard gen == generation, epoch == statusEpoch else { return }
            statusEpoch &+= 1
            status = next
            error = nil
            followID &+= 1
        } catch {
            guard gen == generation, epoch == statusEpoch, !(error is CancellationError) else { return }
            statusEpoch &+= 1
            self.error = FeedError(error)
        }
    }
}

struct KnobDemoRequest: Encodable, Equatable, Sendable {
    var scene: String?
    var scenes: [String]?
    var hold: Bool?
}
