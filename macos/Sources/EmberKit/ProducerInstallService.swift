import Foundation
import os

/// What a `launchctl print gui/<uid>/<label>` result says about the job.
public enum LaunchdProbe: Sendable, Equatable {
    case loaded
    /// Exit 113 / "Could not find service": launchd has no such job.
    case notLoaded
    /// launchd has the job but keeps failing to spawn it
    /// (`launchdJobIsStuck`), and won't recover on its own.
    case stuck
    /// Any other failure: launchctl itself broke, so we can't tell.
    case unknown
}

/// Classifies a `launchctl print` result. Only "no such service" is
/// `.notLoaded`, and only a job `launchdJobIsStuck` recognises is `.stuck`;
/// anything else that fails is `.unknown`, never a reason to re-register.
public func launchdProbe(_ result: CommandResult) -> LaunchdProbe {
    if result.exitCode == 0 { return launchdJobIsStuck(result.stdout) ? .stuck : .loaded }
    if result.exitCode == 113 || (result.stderr + result.stdout).contains("Could not find service") {
        return .notLoaded
    }
    return .unknown
}

/// Whether `launchctl print` output describes a job launchd keeps failing to
/// spawn: not running, `job state = spawn failed`, and either `needs LWCR
/// update` in its properties or `last exit code = 78`. One signal alone isn't
/// enough: right after a changed helper's first spawn, launchd sets `needs
/// LWCR update` while its own repair is still running, and booting the job
/// out then would cut that repair short where it works.
///
/// That's where an ad-hoc signed helper ends up after its code changes.
/// Background Items pins the job's launch constraint (LWCR) to the helper's
/// cdhash, and re-registering keeps the existing item, so the new helper's
/// first spawn is a Launch Constraint Violation. launchd's own repair then
/// makes Background Items replace the item (new UUID, fresh constraint) but
/// reports failure, and the loaded job keeps the old item's UUID: every later
/// spawn exits 78 (EX_CONFIG) until the job is booted out and registered again.
public func launchdJobIsStuck(_ output: String) -> Bool {
    let fields = launchctlPrintFields(output)
    guard fields["state"] != "running", fields["job state"] == "spawn failed" else { return false }
    let needsLWCR = fields["properties"]?.contains("needs LWCR update") ?? false
    let lastExit = fields["last exit code"] ?? ""
    let exitedConfig = lastExit == "78" || lastExit.hasPrefix("78:")
    return needsLWCR || exitedConfig
}

/// The job's own `key = value` lines from `launchctl print` output (one tab
/// deep). Nested blocks, such as a coalition's `state = active`, are skipped.
func launchctlPrintFields(_ output: String) -> [String: String] {
    var fields: [String: String] = [:]
    for line in output.split(separator: "\n") {
        guard line.hasPrefix("\t"), !line.hasPrefix("\t\t"),
              let eq = line.range(of: " = ") else { continue }
        let key = String(line[line.index(after: line.startIndex)..<eq.lowerBound])
        if fields[key] == nil {
            fields[key] = line[eq.upperBound...].trimmingCharacters(in: .whitespaces)
        }
    }
    return fields
}

/// Whether launch should record the new bundle fingerprint after
/// `reconcile(bundleChanged:)`: only when the bundle changed and every
/// re-registration succeeded, so a failure retries on the next launch.
public func shouldRecordFingerprint(bundleChanged: Bool, outcomes: [ReconcileOutcome]) -> Bool {
    bundleChanged && outcomes.allSatisfy { $0.error == nil }
}

/// Whether launch should look at the agents again a little after an update
/// reconcile re-registered some. A changed ad-hoc helper only fails once
/// launchd has tried to spawn it (see `launchdJobIsStuck`), so the job looks
/// fine right after registering and gets stuck seconds later.
public func shouldRecheckAfterReconcile(bundleChanged: Bool, outcomes: [ReconcileOutcome]) -> Bool {
    bundleChanged && outcomes.contains { $0.error == nil }
}

/// How long after an update reconcile the recheck runs: past the new
/// helper's first spawn, launchd's 10 s respawn throttle and its failed
/// constraint repair (about 11 s in all, on macOS 27).
public let producerRecheckDelay: Duration = .seconds(30)

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
    /// launchd has the job but keeps failing to spawn it (`launchdJobIsStuck`).
    /// Registering again on top of it keeps the stuck job, so it's booted out
    /// first.
    case stuck
}

/// What the launchd probe says about an enabled agent's job.
public enum AgentLiveness: Sendable, Equatable {
    case running
    case notLoaded
    case stuck
}

/// Whether an agent needs re-registering, and why. Only an agent the user
/// turned on (`.enabled`) is ever touched.
public func reconcileReason(registration: AgentRegistration, liveness: AgentLiveness,
                            bundleChanged: Bool) -> ReconcileReason? {
    guard registration == .enabled else { return nil }
    if bundleChanged { return .bundleChanged }
    switch liveness {
    case .running: return nil
    case .notLoaded: return .notRunning
    case .stuck: return .stuck
    }
}

/// Errors thrown by `ProducerInstallService` during install/uninstall.
public enum ProducerInstallError: Error, Equatable, Sendable {
    /// The producer binary's `configure` subcommand exited non-zero.
    case configureFailed(exit: Int32)
    /// `launchctl bootout` of a stuck job failed (exit -1: it didn't run).
    case bootoutFailed(exit: Int32, detail: String)
}

extension ProducerInstallError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .configureFailed:
            nil   // unchanged: Foundation's default description
        case .bootoutFailed(let exit, let detail):
            String(localized: "macOS wouldn't stop the stuck background helper (launchctl exit \(exit): \(detail)).",
                   comment: "Settings › Agents failure after Repair when launchctl bootout fails; exit code, then launchctl's message.")
        }
    }
}

/// The per-agent LaunchAgent registration state, derived from
/// `SMAppServiceControlling.status(plistName:)`.
public enum AgentState: Sendable, Equatable {
    case off
    case needsApproval
    case on
    /// Registered and enabled, but launchd has no job for it or can't start
    /// it, so nothing is reporting. `ProducerInstallService.repairAll()` fixes it.
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
    private let readFile: @Sendable (String) -> Data?
    private let uid: uid_t
    private let probeWarned = OSAllocatedUnfairLock(initialState: false)
    /// One install/uninstall/reconcile at a time (see `reconcile`).
    private let serial = SerialGate()
    private static let log = Logger(subsystem: "com.ember.Ember", category: "producers")

    public init(
        sm: SMAppServiceControlling,
        runner: ProducerCommandRunning,
        bundleMacOSDir: URL,
        home: URL,
        fileExists: @escaping @Sendable (String) -> Bool,
        readFile: @escaping @Sendable (String) -> Data? = { FileManager.default.contents(atPath: $0) },
        uid: uid_t = getuid()
    ) {
        self.sm = sm
        self.runner = runner
        self.bundleMacOSDir = bundleMacOSDir
        self.home = home
        self.fileExists = fileExists
        self.readFile = readFile
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

    /// Whether launchd has a job for `agent` in this user's GUI domain, and
    /// can start it. `SMAppService.status` can't tell: it reads the Background
    /// Items database, which stays `.enabled` after launchd drops the job or
    /// stops being able to spawn it. A probe that can't run, or fails any
    /// other way than "no such service", counts as running (logged once), so
    /// a broken probe never churns registrations.
    public func liveness(_ agent: ProducerAgent) -> AgentLiveness {
        let result: CommandResult
        do {
            result = try runner.run(executable: "/bin/launchctl",
                                    arguments: ["print", launchdTarget(agent)])
        } catch {
            warnProbeOnce("launchctl print didn't run: \(error.localizedDescription)")
            return .running
        }
        switch launchdProbe(result) {
        case .loaded: return .running
        case .notLoaded: return .notLoaded
        case .stuck: return .stuck
        case .unknown:
            warnProbeOnce("launchctl print exited \(result.exitCode): \(result.stderr)\(result.stdout)")
            return .running
        }
    }

    private func launchdTarget(_ agent: ProducerAgent) -> String {
        "gui/\(uid)/\(agent.label)"
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
            return liveness(agent) == .running ? .on : .notRunning
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
    /// what failed. Serialized with the other batch operations (`serial`).
    @concurrent
    public func installAll() async -> [AgentOutcome] {
        await serial.run {
            detectedAgents().map { agent in
                do {
                    try install(agent)
                    return AgentOutcome(agent: agent, error: nil)
                } catch {
                    return AgentOutcome(agent: agent, error: error)
                }
            }
        }
    }

    /// Uninstalls every detected agent off the calling actor, catching
    /// per-agent failures so one agent's error never prevents the others from
    /// being attempted. Never throws; inspect each `AgentOutcome.error` to see
    /// what failed. Serialized with the other batch operations (`serial`).
    @concurrent
    public func uninstallAll() async -> [AgentOutcome] {
        await serial.run {
            detectedAgents().map { agent in
                do {
                    try uninstall(agent)
                    return AgentOutcome(agent: agent, error: nil)
                } catch {
                    return AgentOutcome(agent: agent, error: error)
                }
            }
        }
    }

    /// Re-registers (unregister then register) each enabled agent that
    /// `reconcileReason` picks: all of them after an app update, so the newly
    /// bundled helpers take over, otherwise only those launchd has no job for
    /// or can't start. A stuck job is booted out first: registering on top of
    /// it leaves launchd holding the stale job. Runs off the calling actor.
    /// Agents that aren't enabled are never touched. Never throws; one agent's
    /// failure doesn't stop the others.
    ///
    /// Serialized with Repair, install and uninstall (`serial`): the launch
    /// recheck and a Repair click never run bootout/register on the same
    /// label at once, and whichever runs second probes again, sees the healed
    /// job, and does nothing.
    @concurrent
    public func reconcile(bundleChanged: Bool) async -> [ReconcileOutcome] {
        await serial.run { reconcileNow(bundleChanged: bundleChanged) }
    }

    private func reconcileNow(bundleChanged: Bool) -> [ReconcileOutcome] {
        ProducerAgent.allCases.compactMap { agent in
            let registration = sm.status(plistName: agent.plistName)
            let live = registration == .enabled && !bundleChanged ? liveness(agent) : .running
            guard let reason = reconcileReason(registration: registration, liveness: live,
                                               bundleChanged: bundleChanged) else { return nil }
            // Still register when bootout fails (it can't hurt), but report
            // the failure: the job may well stay stuck.
            let bootoutError = reason == .stuck ? bootout(agent) : nil
            do {
                // A stale registration (launchd dropped the job) may refuse
                // to unregister; register is what matters, so go on anyway.
                try? sm.unregister(plistName: agent.plistName)
                try sm.register(plistName: agent.plistName)
                return ReconcileOutcome(agent: agent, reason: reason, error: bootoutError)
            } catch {
                return ReconcileOutcome(agent: agent, reason: reason, error: error)
            }
        }
    }

    /// `launchctl bootout` of the agent's job. "No such service" counts as
    /// done; any other failure is logged and returned.
    private func bootout(_ agent: ProducerAgent) -> ProducerInstallError? {
        let target = launchdTarget(agent)
        let exit: Int32
        let detail: String
        do {
            let result = try runner.run(executable: "/bin/launchctl", arguments: ["bootout", target])
            if bootoutSucceeded(result) { return nil }
            exit = result.exitCode
            detail = (result.stderr + result.stdout).trimmingCharacters(in: .whitespacesAndNewlines)
        } catch {
            exit = -1
            detail = error.localizedDescription
        }
        Self.log.error("launchctl bootout \(target, privacy: .public) failed: exit=\(exit) \(detail, privacy: .public)")
        return .bootoutFailed(exit: exit, detail: detail)
    }

    /// Settings' Repair action: re-registers every enabled agent that isn't
    /// running (booting out a stuck job first), off the calling actor.
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
        let blocked = agents.filter { $0.state == .on && localNetworkBlocked($0.agent) }.map(\.agent)
        return ProducerSnapshot(agents: agents, toggle: Self.toggle(for: agents.map(\.state)),
                                localNetworkBlocked: blocked)
    }

    /// Whether the agent's helper last failed to reach the server with "no
    /// route to host": how macOS denies a LAN connection it hasn't been given
    /// Local Network access for. Read from the file the helper's LaunchAgent
    /// records (`ProducerAgent.linkStatusRelPath`).
    public func localNetworkBlocked(_ agent: ProducerAgent) -> Bool {
        guard let data = readFile(home.appendingPathComponent(agent.linkStatusRelPath).path) else { return false }
        return ProducerLinkState.decode(data)?.noRoute ?? false
    }

    private func executablePath(for agent: ProducerAgent) -> String {
        bundleMacOSDir.appendingPathComponent(agent.binaryName).path
    }
}

/// Whether a `launchctl bootout` result means the job is gone: exit 0, or
/// launchd had no such job (113, or 3 "No such process").
public func bootoutSucceeded(_ result: CommandResult) -> Bool {
    result.exitCode == 0 || result.exitCode == 113 || result.exitCode == 3
        || (result.stderr + result.stdout).contains("Could not find service")
}

/// What a producer helper's LaunchAgent last recorded about reaching the
/// server (Go `producer.LinkState`, in `~/.config/ember/<name>.link.json`).
public struct ProducerLinkState: Decodable, Sendable, Equatable {
    public let ok: Bool
    public let noRoute: Bool

    enum CodingKeys: String, CodingKey {
        case ok
        case noRoute = "no_route"
    }

    public init(ok: Bool, noRoute: Bool) {
        self.ok = ok
        self.noRoute = noRoute
    }

    /// nil for anything that isn't the helper's JSON.
    public static func decode(_ data: Data) -> ProducerLinkState? {
        try? JSONDecoder().decode(ProducerLinkState.self, from: data)
    }
}

/// Runs operations one at a time, in arrival order, without blocking a
/// thread while waiting.
final class SerialGate: Sendable {
    private struct State: Sendable {
        var busy = false
        var waiters: [CheckedContinuation<Void, Never>] = []
    }
    private let state = OSAllocatedUnfairLock(initialState: State())

    func run<T>(_ body: () -> T) async -> T {
        await acquire()
        defer { release() }
        return body()
    }

    private func acquire() async {
        await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
            let proceed = state.withLock { s -> Bool in
                if s.busy {
                    s.waiters.append(continuation)
                    return false
                }
                s.busy = true
                return true
            }
            if proceed { continuation.resume() }
        }
    }

    private func release() {
        let next = state.withLock { s -> CheckedContinuation<Void, Never>? in
            if s.waiters.isEmpty {
                s.busy = false
                return nil
            }
            return s.waiters.removeFirst()
        }
        next?.resume()
    }
}

/// A point-in-time read of the producer agents, from
/// `ProducerInstallService.snapshot()`.
public struct ProducerSnapshot: Sendable {
    /// Detected agents in `ProducerAgent` declaration order, with their state.
    public let agents: [(agent: ProducerAgent, state: AgentState)]
    /// The aggregate toggle state across `agents`.
    public let toggle: ToggleState
    /// Running agents whose helper can't reach the server because macOS
    /// hasn't given it Local Network access.
    public let localNetworkBlocked: [ProducerAgent]

    public init(agents: [(agent: ProducerAgent, state: AgentState)], toggle: ToggleState,
                localNetworkBlocked: [ProducerAgent] = []) {
        self.agents = agents
        self.toggle = toggle
        self.localNetworkBlocked = localNetworkBlocked
    }

    /// Whether an agent is on but not running, so Settings offers Repair.
    public var needsRepair: Bool { agents.contains { $0.state == .notRunning } }
}
