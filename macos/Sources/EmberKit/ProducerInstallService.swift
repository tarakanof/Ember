import Foundation
import os

/// What a `launchctl print gui/<uid>/<label>` result says about the job.
public enum LaunchdProbe: Sendable, Equatable {
    case loaded
    /// Exit 113 / "Could not find service": launchd has no such job.
    case notLoaded
    /// Any other failure: launchctl itself broke, so we can't tell.
    case unknown
}

/// Classifies a `launchctl print` result. Only "no such service" is
/// `.notLoaded`; anything else that fails is `.unknown`, never a reason to
/// re-register.
public func launchdProbe(_ result: CommandResult) -> LaunchdProbe {
    if result.exitCode == 0 { return .loaded }
    if result.exitCode == 113 || (result.stderr + result.stdout).contains("Could not find service") {
        return .notLoaded
    }
    return .unknown
}

/// Whether launch should record the new bundle fingerprint after
/// `reconcile(bundleChanged:)`: only when the bundle changed and every
/// re-registration succeeded, so a failure retries on the next launch.
public func shouldRecordFingerprint(bundleChanged: Bool, outcomes: [ReconcileOutcome]) -> Bool {
    bundleChanged && outcomes.allSatisfy { $0.error == nil }
}

/// Decides whether launch should treat the bundle as updated: re-register
/// every enabled agent's LaunchAgent so the new helpers take over. Re-registering
/// on every launch is needless churn (and can re-surface a "needs approval"
/// state), so the caller compares a fingerprint of the bundle
/// (`bundleFingerprint(appURL:version:build:)`) with the one recorded after the
/// last successful reconcile. `nil` (nothing recorded) reconciles once.
public func shouldReconcileAfterUpdate(currentVersion: String, lastReconciledVersion: String?) -> Bool {
    currentVersion != lastReconciledVersion
}

/// Why `ProducerInstallService.reconcile(bundleChanged:)` re-registers an agent.
public enum ReconcileReason: Sendable, Equatable {
    /// The app bundle (helpers or plists) changed since the last reconcile.
    case bundleChanged
    /// Enabled in Background Items, but launchd has no job for it: booted out
    /// (e.g. by the CLI `uninstall`), or dropped after it couldn't be spawned.
    /// launchd won't bring it back before the next login on its own.
    case notRunning
}

/// Whether an agent needs re-registering, and why. Only an agent the user
/// turned on (`.enabled`) is ever touched.
public func reconcileReason(registration: AgentRegistration, loaded: Bool, bundleChanged: Bool) -> ReconcileReason? {
    guard registration == .enabled else { return nil }
    if bundleChanged { return .bundleChanged }
    return loaded ? nil : .notRunning
}

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
    /// Registered and enabled, but launchd has no job for it, so nothing is
    /// reporting. `ProducerInstallService.repairAll()` fixes it.
    case notRunning
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

/// One agent `reconcile(bundleChanged:)` re-registered. `error` is `nil` on
/// success.
public struct ReconcileOutcome: Sendable {
    public let agent: ProducerAgent
    public let reason: ReconcileReason
    public let error: Error?
}

/// Orchestrates detection, install, and uninstall of the unified installer's
/// producer agents (Claude heartbeat producer, Codex producer). Install
/// shells out to the producer binary's `configure` subcommand, then
/// registers the corresponding LaunchAgent via `SMAppServiceControlling`;
/// if registration fails, it best-effort rolls back the shell-side
/// configuration via `deconfigure`. Uninstall reverses the order:
/// unregister first, then `deconfigure`.
///
/// Every method blocks on process spawns or `SMAppService` IPC, so the type is
/// nonisolated and `Sendable`; the batch operations and `snapshot()` are
/// `@concurrent` so a MainActor caller awaits them without stalling the UI.
/// They still block a cooperative-pool thread while they run (process waits,
/// XPC), which is acceptable for the two agents this manages.
public final class ProducerInstallService: Sendable {
    private let sm: SMAppServiceControlling
    private let runner: ProducerCommandRunning
    private let bundleMacOSDir: URL
    private let home: URL
    private let fileExists: @Sendable (String) -> Bool
    private let uid: uid_t
    private let probeWarned = OSAllocatedUnfairLock(initialState: false)
    private static let log = Logger(subsystem: "com.ember.Ember", category: "producers")

    public init(
        sm: SMAppServiceControlling,
        runner: ProducerCommandRunning,
        bundleMacOSDir: URL,
        home: URL,
        fileExists: @escaping @Sendable (String) -> Bool,
        uid: uid_t = getuid()
    ) {
        self.sm = sm
        self.runner = runner
        self.bundleMacOSDir = bundleMacOSDir
        self.home = home
        self.fileExists = fileExists
        self.uid = uid
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

    /// Whether launchd has a job for `agent` in this user's GUI domain.
    /// `SMAppService.status` can't tell: it reads the Background Items
    /// database, which stays `.enabled` after launchd drops the job. A probe
    /// that can't run, or fails any other way than "no such service", counts
    /// as loaded (logged once), so a broken probe never churns registrations.
    public func isLoaded(_ agent: ProducerAgent) -> Bool {
        let result: CommandResult
        do {
            result = try runner.run(executable: "/bin/launchctl",
                                    arguments: ["print", "gui/\(uid)/\(agent.label)"])
        } catch {
            warnProbeOnce("launchctl print didn't run: \(error.localizedDescription)")
            return true
        }
        switch launchdProbe(result) {
        case .loaded: return true
        case .notLoaded: return false
        case .unknown:
            warnProbeOnce("launchctl print exited \(result.exitCode): \(result.stderr)\(result.stdout)")
            return true
        }
    }

    private func warnProbeOnce(_ message: String) {
        let first = probeWarned.withLock { warned in
            defer { warned = true }
            return !warned
        }
        if first {
            Self.log.warning("producer liveness probe failed; treating agents as running: \(message, privacy: .public)")
        }
    }

    /// Derives the LaunchAgent state for `agent` from `sm.status(plistName:)`,
    /// plus a launchd probe when it's enabled.
    public func agentState(_ agent: ProducerAgent) -> AgentState {
        switch sm.status(plistName: agent.plistName) {
        case .enabled:
            return isLoaded(agent) ? .on : .notRunning
        case .requiresApproval:
            return .needsApproval
        case .notRegistered:
            return .off
        case .notFound:
            return .error(String(localized: "Not installed: the app is missing its launch agent.",
                                  comment: "A producer helper's state in Settings › Agents when its LaunchAgent plist isn't in the app bundle."))
        }
    }

    /// Aggregates `agentState(_:)` across `detectedAgents()` into a single
    /// toggle state (`.notRunning` counts as `.on`: reporting is on, and the
    /// agent's row shows the problem): all `.on` → `.on`; any `.error` → `.error`; else any
    /// `.needsApproval` → `.needsApproval`; a mix of `.on`/`.off` →
    /// `.partial`; all `.off` (or no detected agents) → `.off`.
    public func toggleState() -> ToggleState {
        Self.toggle(for: detectedAgents().map(agentState))
    }

    static func toggle(for agentStates: [AgentState]) -> ToggleState {
        let states = agentStates.map { $0 == .notRunning ? .on : $0 }
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

    /// Installs every detected agent off the calling actor, catching
    /// per-agent failures so one agent's error never prevents the others from
    /// being attempted. Never throws; inspect each `AgentOutcome.error` to see
    /// what failed.
    @concurrent
    public func installAll() async -> [AgentOutcome] {
        detectedAgents().map { agent in
            do {
                try install(agent)
                return AgentOutcome(agent: agent, error: nil)
            } catch {
                return AgentOutcome(agent: agent, error: error)
            }
        }
    }

    /// Uninstalls every detected agent off the calling actor, catching
    /// per-agent failures so one agent's error never prevents the others from
    /// being attempted. Never throws; inspect each `AgentOutcome.error` to see
    /// what failed.
    @concurrent
    public func uninstallAll() async -> [AgentOutcome] {
        detectedAgents().map { agent in
            do {
                try uninstall(agent)
                return AgentOutcome(agent: agent, error: nil)
            } catch {
                return AgentOutcome(agent: agent, error: error)
            }
        }
    }

    /// Re-registers (unregister then register) each enabled agent that
    /// `reconcileReason` picks: all of them after an app update, so the newly
    /// bundled helpers take over, otherwise only those launchd has no job for.
    /// Runs off the calling actor. Agents that aren't enabled are never
    /// touched. Never throws; one agent's failure doesn't stop the others.
    @concurrent
    public func reconcile(bundleChanged: Bool) async -> [ReconcileOutcome] {
        ProducerAgent.allCases.compactMap { agent in
            let registration = sm.status(plistName: agent.plistName)
            let loaded = registration == .enabled && !bundleChanged ? isLoaded(agent) : true
            guard let reason = reconcileReason(registration: registration, loaded: loaded,
                                               bundleChanged: bundleChanged) else { return nil }
            do {
                // A stale registration (launchd dropped the job) may refuse
                // to unregister; register is what matters, so go on anyway.
                try? sm.unregister(plistName: agent.plistName)
                try sm.register(plistName: agent.plistName)
                return ReconcileOutcome(agent: agent, reason: reason, error: nil)
            } catch {
                return ReconcileOutcome(agent: agent, reason: reason, error: error)
            }
        }
    }

    /// Settings' Repair action: re-registers every enabled agent that isn't
    /// running, off the calling actor.
    @concurrent
    public func repairAll() async -> [AgentOutcome] {
        await reconcile(bundleChanged: false).map { AgentOutcome(agent: $0.agent, error: $0.error) }
    }

    /// Reads detection and registration state for every agent off the calling
    /// actor, for a UI that must not do filesystem and `SMAppService` reads
    /// while rendering.
    @concurrent
    public func snapshot() async -> ProducerSnapshot {
        let agents = detectedAgents().map { (agent: $0, state: agentState($0)) }
        return ProducerSnapshot(agents: agents, toggle: Self.toggle(for: agents.map(\.state)))
    }

    private func executablePath(for agent: ProducerAgent) -> String {
        bundleMacOSDir.appendingPathComponent(agent.binaryName).path
    }
}

/// A point-in-time read of the producer agents, from
/// `ProducerInstallService.snapshot()`.
public struct ProducerSnapshot: Sendable {
    /// Detected agents in `ProducerAgent` declaration order, with their state.
    public let agents: [(agent: ProducerAgent, state: AgentState)]
    /// The aggregate toggle state across `agents`.
    public let toggle: ToggleState

    /// Whether an agent is on but not running, so Settings offers Repair.
    public var needsRepair: Bool { agents.contains { $0.state == .notRunning } }
}
