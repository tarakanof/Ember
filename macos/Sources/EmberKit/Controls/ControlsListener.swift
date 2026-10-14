import Foundation
import OSLog

public protocol ControlsCommandSource: Sendable {
    func commands(mac: String, wait: Int) async throws -> [ControlCommand]
}

public protocol ControlsExecutor: Sendable {
    func changeVolume(by points: Int) async -> Bool
    func toggleMute() async -> Bool
    func press(_ key: MediaKey) async -> Bool
    func scroll(_ axis: ScrollAxis, lines: Int) async -> Bool
    func runShortcut(_ name: String) async -> Bool
    func runAppleScript(_ source: String) async -> Bool
    func canPostEvents() async -> Bool
}

public enum ControlOutcome: Equatable, Sendable {
    case ran, failed, stale, needsAccessibility, unassigned, notTarget, cancelled
}

public enum ControlDispatcher {
    public static func perform(_ c: ControlCommand, mapping: ControlsMapping, on executor: ControlsExecutor,
                               age: Duration = .zero) async -> ControlOutcome {
        guard age <= c.lifetime else { return .stale }
        let action = mapping[c.gesture]
        if action.kind.needsEventAccess, !(await executor.canPostEvents()) { return .needsAccessibility }
        let ok: Bool
        switch (c.gesture.isTurn, action.kind) {
        case (true, .volume):
            ok = await executor.changeVolume(by: max(-100, min(100, c.steps * (action.step ?? 2))))
        case (true, .scroll):
            ok = await executor.scroll(action.axis ?? .vertical, lines: max(-100, min(100, c.steps * (action.lines ?? 3))))
        case (false, .mute):
            ok = await executor.toggleMute()
        case (false, .media):
            ok = await executor.press(action.key ?? .playPause)
        case (false, .shortcut):
            guard let name = action.name, !name.trimmingCharacters(in: .whitespaces).isEmpty else { return .unassigned }
            ok = await executor.runShortcut(name)
        case (false, .applescript):
            guard let script = action.script, !script.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
                return .unassigned
            }
            ok = await executor.runAppleScript(script)
        default:
            return .unassigned
        }
        return ok ? .ran : .failed
    }
}

public struct ControlsClient: ControlsCommandSource {
    let client: APIClient
    public init(client: APIClient) { self.client = client }

    public func config() async throws -> ControlsConfig {
        try await client.get("/v1/controls/config")
    }

    public func commands(mac: String, wait: Int) async throws -> [ControlCommand] {
        let answer: ControlCommands = try await client.get("/v1/controls/commands", query: [
            URLQueryItem(name: "mac", value: mac), URLQueryItem(name: "wait", value: String(wait)),
        ], budget: .clockLong)
        return answer.commands
    }
}

@MainActor
public final class ControlsListener {
    public static let wait = 25
    private static let log = Logger(subsystem: "com.ember.Ember", category: "controls")
    static let firstBackoff: Duration = .seconds(5)
    static let maxBackoff: Duration = .seconds(60)
    static let missingRouteBackoff: Duration = .seconds(300)
    static let minRound: Duration = .seconds(1)

    public typealias Sleep = @Sendable (Duration) async throws -> Void

    private let executor: ControlsExecutor
    private let mapping: @MainActor () -> ControlsMapping
    private let isTarget: @MainActor () -> Bool
    private let sleep: Sleep
    private var task: Task<Void, Never>?
    private var loggedNoAccess = false

    public init(executor: ControlsExecutor, mapping: @escaping @MainActor () -> ControlsMapping,
                isTarget: @escaping @MainActor () -> Bool,
                sleep: @escaping Sleep = { try await Task.sleep(for: $0) }) {
        self.executor = executor
        self.mapping = mapping
        self.isTarget = isTarget
        self.sleep = sleep
    }

    public var isRunning: Bool { task != nil }

    public func start(source: ControlsCommandSource, mac: String) {
        stop()
        task = Task { [weak self] in await self?.run(source: source, mac: mac) }
    }

    public func stop() {
        task?.cancel()
        task = nil
    }

    public func join() async { await task?.value }

    private func run(source: ControlsCommandSource, mac: String) async {
        var backoff = Self.firstBackoff
        let clock = ContinuousClock()
        while !Task.isCancelled {
            let began = clock.now
            do {
                let commands = try await source.commands(mac: mac, wait: Self.wait)
                backoff = Self.firstBackoff
                await execute(commands, received: clock.now)
                if commands.isEmpty, clock.now - began < Self.minRound { try await sleep(Self.minRound) }
            } catch {
                if Task.isCancelled || error is CancellationError { return }
                Self.log.notice("knob controls poll failed, nothing run: \(String(describing: error), privacy: .public)")
                let pause: Duration
                switch error as? APIError {
                case .http(404, _)?: pause = Self.missingRouteBackoff
                case .rateLimited(let after)?: pause = max(after, Self.firstBackoff)
                default:
                    pause = backoff
                    backoff = min(backoff * 2, Self.maxBackoff)
                }
                do { try await sleep(pause) } catch { return }
            }
        }
    }

    @discardableResult
    func execute(_ commands: [ControlCommand], received: ContinuousClock.Instant = .now) async -> [ControlOutcome] {
        var outcomes: [ControlOutcome] = []
        for c in commands {
            if Task.isCancelled {
                outcomes.append(.cancelled)
                continue
            }
            guard isTarget() else {
                Self.log.notice("knob control ignored: this Mac is not the target (\(c.gesture.rawValue, privacy: .public))")
                outcomes.append(.notTarget)
                continue
            }
            let age = Duration.milliseconds(c.ageMs) + (ContinuousClock.now - received)
            let outcome = await ControlDispatcher.perform(c, mapping: mapping(), on: executor, age: age)
            outcomes.append(outcome)
            switch outcome {
            case .needsAccessibility:
                if !loggedNoAccess {
                    Self.log.notice("knob control skipped: Accessibility not granted (\(c.gesture.rawValue, privacy: .public))")
                }
                loggedNoAccess = true
            case .stale:
                Self.log.notice("dropped a stale knob control (\(c.gesture.rawValue, privacy: .public))")
            case .failed:
                Self.log.error("knob control failed (\(c.gesture.rawValue, privacy: .public))")
            case .ran:
                if mapping()[c.gesture].kind.needsEventAccess { loggedNoAccess = false }
            case .unassigned, .notTarget, .cancelled:
                break
            }
        }
        return outcomes
    }
}
