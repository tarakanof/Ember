import Foundation

/// Errors thrown by `ProducerInstallService` during install/uninstall.
public enum ProducerInstallError: Error, Equatable, Sendable {
    /// The producer binary's `configure` subcommand exited non-zero.
    case configureFailed(exit: Int32)
}

/// The per-agent LaunchAgent registration state, derived from
/// `SMAppServiceControlling.status(plistName:)`.
public enum AgentState: Sendable, Equatable {
    case off
    case needsApproval
    case on
    case error(String)
}

/// The aggregate toggle state shown in the UI, derived across all
/// *detected* agents (see `ProducerInstallService.detectedAgents()`).
public enum ToggleState: Sendable, Equatable {
    case off
    case needsApproval
    case on
    case partial
    case error
}

/// The result of installing or uninstalling a single agent as part of a
/// batch operation (`installAll`/`uninstallAll`). `error` is `nil` on
/// success.
public struct AgentOutcome: Sendable {
    public let agent: ProducerAgent
    public let error: Error?
}

/// Orchestrates detection, install, and uninstall of the unified installer's
/// producer agents (Claude heartbeat producer, Codex producer). Install
/// shells out to the producer binary's `configure` subcommand, then
/// registers the corresponding LaunchAgent via `SMAppServiceControlling`;
/// if registration fails, it best-effort rolls back the shell-side
/// configuration via `deconfigure`. Uninstall reverses the order:
/// unregister first, then `deconfigure`.
@MainActor
public final class ProducerInstallService {
    private let sm: SMAppServiceControlling
    private let runner: ProducerCommandRunning
    private let bundleMacOSDir: URL
    private let home: URL
    private let fileExists: @Sendable (String) -> Bool

    public init(
        sm: SMAppServiceControlling,
        runner: ProducerCommandRunning,
        bundleMacOSDir: URL,
        home: URL,
        fileExists: @escaping @Sendable (String) -> Bool
    ) {
        self.sm = sm
        self.runner = runner
        self.bundleMacOSDir = bundleMacOSDir
        self.home = home
        self.fileExists = fileExists
    }

    /// Returns the subset of `ProducerAgent` cases whose detection marker
    /// (`$HOME/<detectRelPath>`) exists on disk, in `ProducerAgent`'s
    /// declaration order.
    public func detectedAgents() -> [ProducerAgent] {
        ProducerAgent.allCases.filter { agent in
            fileExists(home.appendingPathComponent(agent.detectRelPath).path)
        }
    }

    /// Runs the producer binary's `configure` subcommand, then registers its
    /// LaunchAgent. If registration throws, best-effort runs `deconfigure`
    /// to roll back the shell-side configuration, then rethrows.
    public func install(_ agent: ProducerAgent) throws {
        let result = try runner.run(executable: executablePath(for: agent), arguments: ["configure"])
        guard result.exitCode == 0 else {
            throw ProducerInstallError.configureFailed(exit: result.exitCode)
        }

        do {
            try sm.register(plistName: agent.plistName)
        } catch {
            _ = try? runner.run(executable: executablePath(for: agent), arguments: ["deconfigure"])
            throw error
        }
    }

    /// Unregisters the LaunchAgent, then runs the producer binary's
    /// `deconfigure` subcommand. Leaves `producer.env` untouched; that's
    /// `deconfigure`'s responsibility.
    public func uninstall(_ agent: ProducerAgent) throws {
        try sm.unregister(plistName: agent.plistName)
        _ = try runner.run(executable: executablePath(for: agent), arguments: ["deconfigure"])
    }

    /// Derives the LaunchAgent registration state for `agent` from
    /// `sm.status(plistName:)`.
    public func agentState(_ agent: ProducerAgent) -> AgentState {
        switch sm.status(plistName: agent.plistName) {
        case .enabled:
            return .on
        case .requiresApproval:
            return .needsApproval
        case .notRegistered:
            return .off
        case .notFound:
            return .error("plist not found")
        }
    }

    /// Aggregates `agentState(_:)` across `detectedAgents()` into a single
    /// toggle state: all `.on` → `.on`; any `.error` → `.error`; else any
    /// `.needsApproval` → `.needsApproval`; a mix of `.on`/`.off` →
    /// `.partial`; all `.off` (or no detected agents) → `.off`.
    public func toggleState() -> ToggleState {
        let states = detectedAgents().map(agentState)
        guard !states.isEmpty else { return .off }

        if states.allSatisfy({ $0 == .on }) {
            return .on
        }
        if states.contains(where: { if case .error = $0 { return true } else { return false } }) {
            return .error
        }
        if states.contains(.needsApproval) {
            return .needsApproval
        }
        if states.contains(.on) {
            return .partial
        }
        return .off
    }

    /// Installs every detected agent, catching per-agent failures so one
    /// agent's error never prevents the others from being attempted. Never
    /// throws; inspect each `AgentOutcome.error` to see what failed.
    public func installAll() -> [AgentOutcome] {
        detectedAgents().map { agent in
            do {
                try install(agent)
                return AgentOutcome(agent: agent, error: nil)
            } catch {
                return AgentOutcome(agent: agent, error: error)
            }
        }
    }

    /// Uninstalls every detected agent, catching per-agent failures so one
    /// agent's error never prevents the others from being attempted. Never
    /// throws; inspect each `AgentOutcome.error` to see what failed.
    public func uninstallAll() -> [AgentOutcome] {
        detectedAgents().map { agent in
            do {
                try uninstall(agent)
                return AgentOutcome(agent: agent, error: nil)
            } catch {
                return AgentOutcome(agent: agent, error: error)
            }
        }
    }

    /// Re-registers every already-`.enabled` agent (unregister then
    /// register) so a newly bundled binary takes over after an app update.
    /// Agents that aren't currently enabled are left untouched.
    public func reconcileAfterUpdate() throws {
        for agent in ProducerAgent.allCases where sm.status(plistName: agent.plistName) == .enabled {
            try sm.unregister(plistName: agent.plistName)
            try sm.register(plistName: agent.plistName)
        }
    }

    private func executablePath(for agent: ProducerAgent) -> String {
        bundleMacOSDir.appendingPathComponent(agent.binaryName).path
    }
}
