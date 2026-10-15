import Foundation
import OSLog

public enum ScriptLaunch: Equatable, Sendable { case launched, busy, failed }

public final class ScriptProcessRunner: @unchecked Sendable {
    typealias Signal = @Sendable (pid_t, Int32) -> Int32

    private static let log = Logger(subsystem: "com.ember.Ember", category: "controls")
    static let killGrace: Duration = .seconds(1)
    static let groupPoll: Duration = .milliseconds(50)

    private let timeout: Duration
    private let signal: Signal
    private let lock = NSLock()
    private var running: Process?
    private var watchdog: Task<Void, Never>?
    private var closed = false

    public convenience init(timeout: Duration = .seconds(10)) {
        self.init(timeout: timeout, signal: { kill($0, $1) })
    }

    init(timeout: Duration, signal: @escaping Signal) {
        self.timeout = timeout
        self.signal = signal
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
        let claimed: Bool? = lock.withLock {
            guard !closed else { return nil }
            guard running == nil else { return false }
            running = p
            return true
        }
        guard let claimed else {
            Self.log.notice("knob \(what, privacy: .public) dropped: Ember is quitting")
            return .failed
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
        if lock.withLock({ closed }) {
            _ = Self.deliver(SIGKILL, to: p.processIdentifier, of: p, signal)
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
        let send = signal
        let dog = Task.detached {
            guard (try? await Task.sleep(for: limit)) != nil, p.isRunning else { return }
            Self.log.notice("knob \(what, privacy: .public) killed after \(limit.components.seconds) s")
            guard Self.deliver(SIGTERM, to: group, of: p, send) else { return }
            Task.detached {
                let clock = ContinuousClock()
                let deadline = clock.now + Self.killGrace
                while clock.now < deadline {
                    try? await Task.sleep(for: Self.groupPoll)
                    if send(-group, 0) != 0, !p.isRunning { return }
                }
                _ = Self.deliver(SIGKILL, to: group, of: p, send)
            }
        }
        lock.withLock {
            if running === p { watchdog = dog } else { dog.cancel() }
        }
        return .launched
    }

    public func stop(grace: Duration = .milliseconds(500)) {
        let (p, dog): (Process?, Task<Void, Never>?) = lock.withLock {
            closed = true
            return (running, watchdog)
        }
        dog?.cancel()
        guard let p, p.processIdentifier > 0 else { return }
        let group = p.processIdentifier
        guard Self.deliver(SIGTERM, to: group, of: p, signal) else { return }
        let clock = ContinuousClock()
        let deadline = clock.now + grace
        while clock.now < deadline {
            if signal(-group, 0) != 0, !p.isRunning { return }
            Thread.sleep(forTimeInterval: 0.01)
        }
        _ = Self.deliver(SIGKILL, to: group, of: p, signal)
    }

    private static func deliver(_ sig: Int32, to group: pid_t, of p: Process, _ send: Signal) -> Bool {
        if send(-group, sig) == 0 { return true }
        return p.isRunning && send(group, sig) == 0
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
