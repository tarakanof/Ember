import Foundation
import OSLog

public enum ScriptLaunch: Equatable, Sendable { case launched, busy, failed }

public final class ScriptProcessRunner: @unchecked Sendable {
    private static let log = Logger(subsystem: "com.ember.Ember", category: "controls")

    private let timeout: Duration
    private let lock = NSLock()
    private var running: Process?
    private var watchdog: Task<Void, Never>?

    public init(timeout: Duration = .seconds(10)) {
        self.timeout = timeout
    }

    public var isBusy: Bool { lock.withLock { running != nil } }

    public func run(_ tool: String, _ args: [String], stdin: Data? = nil, what: String) -> ScriptLaunch {
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
        let group = p.processIdentifier
        let dog = Task.detached {
            guard (try? await Task.sleep(for: limit)) != nil, p.isRunning else { return }
            Self.log.notice("knob \(what, privacy: .public) killed after \(limit.components.seconds) s")
            Self.signal(group, of: p, SIGTERM)
            Task.detached {
                try? await Task.sleep(for: .seconds(1))
                Self.signal(group, of: p, SIGKILL)
            }
        }
        lock.withLock {
            if running === p { watchdog = dog } else { dog.cancel() }
        }
        return .launched
    }

    private static func signal(_ group: pid_t, of p: Process, _ sig: Int32) {
        if kill(-group, sig) != 0, p.isRunning { kill(group, sig) }
    }

    private func finished(_ p: Process) {
        let dog: Task<Void, Never>? = lock.withLock {
            guard running === p else { return nil }
            running = nil
            defer { watchdog = nil }
            return watchdog
        }
        dog?.cancel()
    }
}
