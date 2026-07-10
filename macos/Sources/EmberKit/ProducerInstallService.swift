import Foundation

/// Errors thrown by `ProducerInstallService` during install/uninstall.
public enum ProducerInstallError: Error, Equatable, Sendable {
    /// The producer binary's `configure` subcommand exited non-zero.
    case configureFailed(exit: Int32)
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

    private func executablePath(for agent: ProducerAgent) -> String {
        bundleMacOSDir.appendingPathComponent(agent.binaryName).path
    }
}
