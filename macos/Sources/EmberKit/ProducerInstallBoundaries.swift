import Foundation
import ServiceManagement

public enum AgentRegistration: Sendable, Equatable {
    case notRegistered
    case enabled
    case requiresApproval
    case notFound
}

public protocol SMAppServiceControlling: Sendable {
    func register(plistName: String) throws
    func unregister(plistName: String) throws
    func status(plistName: String) -> AgentRegistration
}

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

public protocol ProducerCommandRunning: Sendable {
    func run(executable: String, arguments: [String]) throws -> CommandResult
}

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

        let stderrBox = DataBox()
        let drained = DispatchGroup()
        DispatchQueue.global(qos: .userInitiated).async(group: drained) {
            stderrBox.data = stderrPipe.fileHandleForReading.readDataToEndOfFile()
        }
        let stdoutData = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
        drained.wait()
        let stderrData = stderrBox.data

        process.waitUntilExit()

        return CommandResult(
            exitCode: process.terminationStatus,
            stdout: String(decoding: stdoutData, as: UTF8.self),
            stderr: String(decoding: stderrData, as: UTF8.self)
        )
    }
}

private final class DataBox: @unchecked Sendable {
    var data = Data()
}
