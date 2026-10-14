import Foundation
import OSLog

public final class ScriptProcessRunner: @unchecked Sendable {
    public enum Result: Equatable, Sendable { case launched, busy, failed }

    private static let log = Logger(subsystem: "com.ember.Ember", category: "controls")

    private let timeout: Duration
    private let lock = NSLock()
    private var running: Process?

    public init(timeout: Duration = .seconds(10)) {
        self.timeout = timeout
    }

    public var isBusy: Bool { lock.withLock { running != nil } }

    public func run(_ tool: String, _ args: [String], stdin: Data? = nil, what: String) -> Result {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: tool)
        p.arguments = args
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        let pipe = stdin.map { _ in Pipe() }
        if let pipe {
            p.standardInput = pipe
            _ = fcntl(pipe.fileHandleForWriting.fileDescriptor, F_SETNOSIGPIPE, 1)
        }
        p.terminationHandler = { [weak self] proc in
            self?.finished(proc)
            if proc.terminationReason == .uncaughtSignal {
                Self.log.notice("knob \(what, privacy: .public) was stopped")
            } else if proc.terminationStatus != 0 {
                Self.log.notice("knob \(what, privacy: .public) exited \(proc.terminationStatus)")
            }
        }
        let claimed = lock.withLock {
            guard running == nil else { return false }
            running = p
            return true
        }
        guard claimed else {
            Self.log.notice("knob \(what, privacy: .public) dropped: another script is still running")
            return .busy
        }
        do {
            try p.run()
        } catch {
            finished(p)
            Self.log.error("knob \(what, privacy: .public) did not start: \(error.localizedDescription, privacy: .public)")
            return .failed
        }
        if let pipe, let stdin {
            let handle = pipe.fileHandleForWriting
            DispatchQueue.global(qos: .userInitiated).async {
                try? handle.write(contentsOf: stdin)
                try? handle.close()
            }
        }
        let limit = timeout
        Task.detached {
            try? await Task.sleep(for: limit)
            if p.isRunning {
                Self.log.notice("knob \(what, privacy: .public) killed after \(limit.components.seconds) s")
                p.terminate()
                try? await Task.sleep(for: .seconds(1))
                if p.isRunning { kill(p.processIdentifier, SIGKILL) }
            }
        }
        return .launched
    }

    private func finished(_ p: Process) {
        lock.withLock {
            if running === p { running = nil }
        }
    }
}
