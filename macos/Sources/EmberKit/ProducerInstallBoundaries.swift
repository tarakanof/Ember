import Foundation
import ServiceManagement

/// The observable registration state of a LaunchAgent, mirroring
/// `SMAppService.Status` in a form that's easy to unit test against.
public enum AgentRegistration: Sendable, Equatable {
    case notRegistered
    case enabled
    case requiresApproval
    case notFound
}

/// Boundary over `SMAppService` so registration logic can be unit tested
/// without touching the real system LaunchAgent database.
public protocol SMAppServiceControlling: Sendable {
    func register(plistName: String) throws
    func unregister(plistName: String) throws
    func status(plistName: String) -> AgentRegistration
}

/// The result of running an external command.
public struct CommandResult: Sendable {
    public let exitCode: Int32
    public let stdout: String
    public let stderr: String

    public init(exitCode: Int32, stdout: String, stderr: String) {
        self.exitCode = exitCode
        self.stdout = stdout
        self.stderr = stderr
    }
}

/// Boundary over running an external command (e.g. a producer binary's
/// `configure`/`deconfigure` subcommand) so callers can be unit tested
/// without spawning real processes.
public protocol ProducerCommandRunning: Sendable {
    func run(executable: String, arguments: [String]) throws -> CommandResult
}

// MARK: - Real adapters (not unit-tested here; validated on-device)

/// Wraps `SMAppService.agent(plistName:)` to register/unregister/query the
/// real per-user LaunchAgent.
public struct RealSMAppService: SMAppServiceControlling {
    public init() {}

    public func register(plistName: String) throws {
        try SMAppService.agent(plistName: plistName).register()
    }

    public func unregister(plistName: String) throws {
        try SMAppService.agent(plistName: plistName).unregister()
    }

    public func status(plistName: String) -> AgentRegistration {
        switch SMAppService.agent(plistName: plistName).status {
        case .notRegistered:
            return .notRegistered
        case .enabled:
            return .enabled
        case .requiresApproval:
            return .requiresApproval
        case .notFound:
            return .notFound
        @unknown default:
            return .notFound
        }
    }
}

/// Wraps `Foundation.Process` to run a command and capture its output.
public struct ProcessCommandRunner: ProducerCommandRunning {
    public init() {}

    public func run(executable: String, arguments: [String]) throws -> CommandResult {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe

        try process.run()

        let stdoutData = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
        let stderrData = stderrPipe.fileHandleForReading.readDataToEndOfFile()

        process.waitUntilExit()

        return CommandResult(
            exitCode: process.terminationStatus,
            stdout: String(decoding: stdoutData, as: UTF8.self),
            stderr: String(decoding: stderrData, as: UTF8.self)
        )
    }
}
