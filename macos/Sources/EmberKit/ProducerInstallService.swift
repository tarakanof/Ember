import Foundation
import os

public enum LaunchdProbe: Sendable, Equatable {
    case loaded
    case notLoaded
    case stuck
    case unknown
}

public func launchdProbe(_ result: CommandResult) -> LaunchdProbe {
    if result.exitCode == 0 { return launchdJobIsStuck(result.stdout) ? .stuck : .loaded }
    if result.exitCode == 113 || (result.stderr + result.stdout).contains("Could not find service") {
        return .notLoaded
    }
    return .unknown
}

public func launchdJobIsStuck(_ output: String) -> Bool {
    let fields = launchctlPrintFields(output)
    guard fields["state"] != "running", fields["job state"] == "spawn failed" else { return false }
    let needsLWCR = fields["properties"]?.contains("needs LWCR update") ?? false
    let lastExit = fields["last exit code"] ?? ""
    let exitedConfig = lastExit == "78" || lastExit.hasPrefix("78:")
    return needsLWCR || exitedConfig
}

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

public func shouldRecordFingerprint(bundleChanged: Bool, outcomes: [ReconcileOutcome]) -> Bool {
    bundleChanged && outcomes.allSatisfy { $0.error == nil }
}

public func shouldRecheckAfterReconcile(bundleChanged: Bool, outcomes: [ReconcileOutcome]) -> Bool {
    bundleChanged && outcomes.contains { $0.error == nil }
}

/// About 11 s: the new helper's first spawn, launchd's 10 s respawn throttle and its failed constraint repair (macOS 27).
public let producerRecheckDelay: Duration = .seconds(30)

public func shouldReconcileAfterUpdate(currentVersion: String, lastReconciledVersion: String?) -> Bool {
    currentVersion != lastReconciledVersion
}

public enum ReconcileReason: Sendable, Equatable {
    case bundleChanged
    case notRunning
    case stuck
}

public enum AgentLiveness: Sendable, Equatable {
    case running
    case notLoaded
    case stuck
}

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

public enum ProducerInstallError: Error, Equatable, Sendable {
    case configureFailed(exit: Int32, detail: String)
    case cliInstalled
    case cliUninstallFailed(exit: Int32, detail: String)
    case moveFailed(helper: String, reason: String, restored: Bool)
    case settingsUnreadable
    case bootoutFailed(exit: Int32, detail: String)
}

extension ProducerInstallError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .configureFailed(let exit, let detail):
            detail.isEmpty
                ? String(localized: "The helper's setup failed (exit \(exit)).",
                         comment: "Settings › Agents failure when a producer helper's configure exits non-zero without a message; the exit code.")
                : detail
        case .cliInstalled:
            String(localized: "It's installed from the command line. Use Move to Ember first.",
                   comment: "Settings › Agents failure when turning on an agent whose CLI LaunchAgent is loaded.")
        case .cliUninstallFailed(let exit, let detail):
            String(localized: "Couldn't remove the command-line agent (exit \(exit): \(detail)); it's still reporting.",
                   comment: "Settings › Agents failure when Move to Ember can't uninstall the CLI agent; exit code, then the helper's message.")
        case .moveFailed(_, let reason, true):
            String(localized: "Ember couldn't take over (\(reason)), so the command-line agent was reinstalled and keeps reporting.",
                   comment: "Settings › Agents failure after Move to Ember when the app's copy failed and the CLI agent was restored; the failure.")
        case .moveFailed(let helper, let reason, false):
            String(localized: "Ember couldn't take over (\(reason)) and the command-line agent is gone, so this agent isn't reporting. Turn it on again here, or run \(helper) install in Terminal.",
                   comment: "Settings › Agents failure after Move to Ember when neither the app's copy nor the CLI agent is installed; the failure, then the helper's name.")
        case .settingsUnreadable:
            String(localized: "~/.claude/settings.json isn't valid JSON. Fix the file by hand first.",
                   comment: "Settings › Agents failure when moving Claude while its settings file can't be parsed.")
        case .bootoutFailed(let exit, let detail):
            String(localized: "macOS wouldn't stop the stuck background helper (launchctl exit \(exit): \(detail)).",
                   comment: "Settings › Agents failure after Repair when launchctl bootout fails; exit code, then launchctl's message.")
        }
    }
}

public enum AgentState: Sendable, Equatable {
    case off
    case needsApproval
    case on
    case notRunning
    case cliInstalled
    case error(String)
}

public enum ToggleState: Sendable, Equatable {
    case off
    case needsApproval
    case on
    case partial
    case error
}

public struct AgentOutcome: Sendable {
    public let agent: ProducerAgent
    public let error: Error?
}

public struct ReconcileOutcome: Sendable {
    public let agent: ProducerAgent
    public let reason: ReconcileReason
    public let error: Error?
}

public final class ProducerInstallService: Sendable {
    private let sm: SMAppServiceControlling
    private let runner: ProducerCommandRunning
    private let bundleMacOSDir: URL
    private let home: URL
    private let fileExists: @Sendable (String) -> Bool
    private let readFile: @Sendable (String) -> Data?
    private let prefs: ProducerPrefsStoring
    private let uid: uid_t
    private let probeWarned = OSAllocatedUnfairLock(initialState: false)
    private let serial = SerialGate()
    private static let log = Logger(subsystem: "com.ember.Ember", category: "producers")

    public init(
        sm: SMAppServiceControlling,
        runner: ProducerCommandRunning,
        bundleMacOSDir: URL,
        home: URL,
        fileExists: @escaping @Sendable (String) -> Bool,
        readFile: @escaping @Sendable (String) -> Data? = { FileManager.default.contents(atPath: $0) },
        prefs: ProducerPrefsStoring = InMemoryProducerPrefs(),
        uid: uid_t = getuid()
    ) {
        self.sm = sm
        self.runner = runner
        self.bundleMacOSDir = bundleMacOSDir
        self.home = home
        self.fileExists = fileExists
        self.readFile = readFile
        self.prefs = prefs
        self.uid = uid
    }

    public func detectedAgents() -> [ProducerAgent] {
        ProducerAgent.allCases.filter(isDetected)
    }

    private func isDetected(_ agent: ProducerAgent) -> Bool {
        var candidates = [home.appendingPathComponent(agent.detectRelPath).path]
        if agent == .t3, let custom = producerEnvValue("EMBER_T3_HOME"), !custom.isEmpty {
            candidates.append(custom.hasPrefix("~/")
                ? home.appendingPathComponent(String(custom.dropFirst(2))).path : custom)
        }
        return candidates.contains(where: fileExists)
    }

    private func producerEnvValue(_ key: String) -> String? {
        guard let data = readFile(home.appendingPathComponent(".config/ember/producer.env").path) else { return nil }
        return EnvFile(parsing: String(decoding: data, as: UTF8.self)).get(key)
    }

    public func managedAgents() -> [ProducerAgent] {
        ProducerAgent.allCases.filter { isRegistered($0) || (isWanted($0) && !isCLIInstalled($0)) }
    }

    private func isWanted(_ agent: ProducerAgent) -> Bool {
        isDetected(agent) && !prefs.optOut.contains(agent.rawValue)
    }

    private func cliPlistPath(_ agent: ProducerAgent) -> String {
        home.appendingPathComponent("Library/LaunchAgents/\(agent.plistName)").path
    }

    private func isCLIInstalled(_ agent: ProducerAgent) -> Bool {
        sm.status(plistName: agent.plistName) == .notRegistered && fileExists(cliPlistPath(agent))
    }

    @concurrent
    public func seedOptOutForNewAgents() async {
        await serial.run { seedNow() }
    }

    private func seedNow() {
        let known = Set(prefs.knownAgents ?? [ProducerAgent.claude.rawValue, ProducerAgent.codex.rawValue])
        let added = ProducerAgent.allCases.filter { !known.contains($0.rawValue) }
        if !added.isEmpty, ProducerAgent.allCases.contains(where: isRegistered) {
            prefs.optOut.formUnion(added.map(\.rawValue))
        }
        prefs.knownAgents = ProducerAgent.allCases.map(\.rawValue)
    }

    private func isRegistered(_ agent: ProducerAgent) -> Bool {
        [.enabled, .requiresApproval].contains(sm.status(plistName: agent.plistName))
    }

    public func install(_ agent: ProducerAgent) throws {
        guard !isCLIInstalled(agent) else { throw ProducerInstallError.cliInstalled }
        try configure(agent)

        do {
            try sm.register(plistName: agent.plistName)
        } catch {
            _ = try? runner.run(executable: executablePath(for: agent), arguments: ["deconfigure"])
            throw error
        }
    }

    private func configure(_ agent: ProducerAgent) throws {
        let result = try runner.run(executable: executablePath(for: agent), arguments: ["configure"])
        guard result.exitCode == 0 else {
            throw ProducerInstallError.configureFailed(
                exit: result.exitCode, detail: result.stderr.trimmingCharacters(in: .whitespacesAndNewlines))
        }
    }

    public func uninstall(_ agent: ProducerAgent) throws {
        try sm.unregister(plistName: agent.plistName)
        _ = try runner.run(executable: executablePath(for: agent), arguments: ["deconfigure"])
    }

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

    @concurrent
    public func restart(_ agent: ProducerAgent) async -> Bool {
        await serial.run {
            guard sm.status(plistName: agent.plistName) == .enabled else { return false }
            let target = launchdTarget(agent)
            guard let probe = try? runner.run(executable: "/bin/launchctl", arguments: ["print", target]),
                  launchdProbe(probe) == .loaded else { return false }
            do {
                let result = try runner.run(executable: "/bin/launchctl", arguments: ["kickstart", "-k", target])
                if result.exitCode == 0 { return true }
                Self.log.warning("launchctl kickstart \(target, privacy: .public) failed: exit=\(result.exitCode) \(result.stderr, privacy: .public)")
            } catch {
                Self.log.warning("launchctl kickstart \(target, privacy: .public) didn't run: \(error.localizedDescription, privacy: .public)")
            }
            return false
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

    public func agentState(_ agent: ProducerAgent) -> AgentState {
        switch sm.status(plistName: agent.plistName) {
        case .enabled:
            return liveness(agent) == .running ? .on : .notRunning
        case .requiresApproval:
            return .needsApproval
        case .notRegistered:
            return fileExists(cliPlistPath(agent)) ? .cliInstalled : .off
        case .notFound:
            return .error(String(localized: "Not installed: the app is missing its launch agent.",
                                  comment: "A producer helper's state in Settings › Agents when its LaunchAgent plist isn't in the app bundle."))
        }
    }

    public func toggleState() -> ToggleState {
        Self.toggle(for: managedAgents().map(agentState))
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

    @concurrent
    public func installAll() async -> [AgentOutcome] {
        await serial.run {
            var wanted = ProducerAgent.allCases.filter(isWanted)
            if wanted.isEmpty {
                prefs.optOut.subtract(detectedAgents().map(\.rawValue))
                wanted = ProducerAgent.allCases.filter(isWanted)
            }
            let installable = wanted.filter { !isCLIInstalled($0) }
            if installable.isEmpty {
                return wanted.map { AgentOutcome(agent: $0, error: ProducerInstallError.cliInstalled) }
            }
            return installable.map { agent in
                do {
                    try install(agent)
                    return AgentOutcome(agent: agent, error: nil)
                } catch {
                    return AgentOutcome(agent: agent, error: error)
                }
            }
        }
    }

    @concurrent
    public func uninstallAll() async -> [AgentOutcome] {
        await serial.run {
            managedAgents().map { agent in
                do {
                    try uninstall(agent)
                    return AgentOutcome(agent: agent, error: nil)
                } catch {
                    return AgentOutcome(agent: agent, error: error)
                }
            }
        }
    }

    @concurrent
    public func setEnabled(_ agent: ProducerAgent, _ on: Bool) async -> [AgentOutcome] {
        await serial.run {
            if on { prefs.optOut.remove(agent.rawValue) } else { prefs.optOut.insert(agent.rawValue) }
            do {
                try on ? install(agent) : uninstall(agent)
                return [AgentOutcome(agent: agent, error: nil)]
            } catch {
                return [AgentOutcome(agent: agent, error: error)]
            }
        }
    }

    @concurrent
    public func moveToEmber(_ agent: ProducerAgent) async -> [AgentOutcome] {
        await serial.run {
            do {
                try moveNow(agent)
                return [AgentOutcome(agent: agent, error: nil)]
            } catch {
                return [AgentOutcome(agent: agent, error: error)]
            }
        }
    }

    private func moveNow(_ agent: ProducerAgent) throws {
        if agent == .claude, claudeHookRegistration().settingsUnreadable {
            throw ProducerInstallError.settingsUnreadable
        }
        let cliBinary = cliProgram(agent)
        let result = try runner.run(executable: executablePath(for: agent), arguments: ["uninstall"])
        guard result.exitCode == 0 else {
            throw ProducerInstallError.cliUninstallFailed(
                exit: result.exitCode, detail: result.stderr.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        do {
            try install(agent)
            prefs.optOut.remove(agent.rawValue)
        } catch ProducerInstallError.cliInstalled {
            throw ProducerInstallError.cliInstalled
        } catch {
            let restored = cliBinary.map { bin in
                fileExists(bin) && ((try? runner.run(executable: bin, arguments: ["install"]))?.exitCode == 0)
            } ?? false
            throw ProducerInstallError.moveFailed(helper: agent.binaryName, reason: error.localizedDescription,
                                                  restored: restored)
        }
    }

    private func cliProgram(_ agent: ProducerAgent) -> String? {
        guard let data = readFile(cliPlistPath(agent)),
              let plist = try? PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
              let args = plist["ProgramArguments"] as? [String] else { return nil }
        return args.first
    }

    @concurrent
    public func configureClaudeHooks() async -> [AgentOutcome] {
        await serial.run {
            do {
                try configure(.claude)
                return [AgentOutcome(agent: .claude, error: nil)]
            } catch {
                return [AgentOutcome(agent: .claude, error: error)]
            }
        }
    }

    public func claudeHookRegistration() -> ClaudeHookRegistration {
        ClaudeHookRegistration.read(home: home, readFile: readFile, fileExists: fileExists)
    }

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
            let bootoutError = reason == .stuck ? bootout(agent) : nil
            do {
                try? sm.unregister(plistName: agent.plistName)
                try sm.register(plistName: agent.plistName)
                return ReconcileOutcome(agent: agent, reason: reason, error: bootoutError)
            } catch {
                return ReconcileOutcome(agent: agent, reason: reason, error: error)
            }
        }
    }

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

    @concurrent
    public func repairAll() async -> [AgentOutcome] {
        await reconcile(bundleChanged: false).map { AgentOutcome(agent: $0.agent, error: $0.error) }
    }

    @concurrent
    public func snapshot() async -> ProducerSnapshot {
        let managedSet = Set(managedAgents())
        let rows = ProducerAgent.allCases.compactMap { agent -> (agent: ProducerAgent, state: AgentState, detected: Bool, managed: Bool)? in
            let detected = isDetected(agent)
            let state = agentState(agent)
            guard detected || state != .off || agent.listedWhenUndetected else { return nil }
            return (agent, state, detected, managedSet.contains(agent))
        }
        let agents = rows.map { (agent: $0.agent, state: $0.state) }
        let managed = rows.filter(\.managed).map(\.state)
        let blocked = agents.filter { $0.state == .on && localNetworkBlocked($0.agent) }.map(\.agent)
        let undetected = Set(rows.filter { !$0.detected }.map(\.agent))
        let hooks = agents.contains { $0.agent == .claude } ? claudeHookRegistration() : nil
        return ProducerSnapshot(agents: agents, toggle: Self.toggle(for: managed),
                                localNetworkBlocked: blocked, undetected: undetected, claudeHooks: hooks)
    }

    public func localNetworkBlocked(_ agent: ProducerAgent) -> Bool {
        guard let data = readFile(home.appendingPathComponent(agent.linkStatusRelPath).path) else { return false }
        return ProducerLinkState.decode(data)?.noRoute ?? false
    }

    private func executablePath(for agent: ProducerAgent) -> String {
        bundleMacOSDir.appendingPathComponent(agent.binaryName).path
    }
}

public func bootoutSucceeded(_ result: CommandResult) -> Bool {
    result.exitCode == 0 || result.exitCode == 113 || result.exitCode == 3
        || (result.stderr + result.stdout).contains("Could not find service")
}

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

    public static func decode(_ data: Data) -> ProducerLinkState? {
        try? JSONDecoder().decode(ProducerLinkState.self, from: data)
    }
}

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

public struct ProducerSnapshot: Sendable {
    public let agents: [(agent: ProducerAgent, state: AgentState)]
    public let toggle: ToggleState
    public let localNetworkBlocked: [ProducerAgent]
    public let undetected: Set<ProducerAgent>
    public let claudeHooks: ClaudeHookRegistration?

    public init(agents: [(agent: ProducerAgent, state: AgentState)], toggle: ToggleState,
                localNetworkBlocked: [ProducerAgent] = [], undetected: Set<ProducerAgent> = [],
                claudeHooks: ClaudeHookRegistration? = nil) {
        self.agents = agents
        self.toggle = toggle
        self.localNetworkBlocked = localNetworkBlocked
        self.undetected = undetected
        self.claudeHooks = claudeHooks
    }

    public var noToolDetected: Bool { agents.allSatisfy { undetected.contains($0.agent) } }

    public var claudeHooksNotice: ClaudeHooksNotice {
        guard let hooks = claudeHooks,
              let claude = agents.first(where: { $0.agent == .claude }) else { return .fine }
        return ClaudeHooksNotice.notice(for: hooks, reportingOn: claude.state != .off)
    }

    public var needsRepair: Bool { agents.contains { $0.state == .notRunning } }

    public func showsSettings(for agent: ProducerAgent) -> Bool {
        guard let row = agents.first(where: { $0.agent == agent }) else { return false }
        return !undetected.contains(agent) || row.state != .off
    }
}
