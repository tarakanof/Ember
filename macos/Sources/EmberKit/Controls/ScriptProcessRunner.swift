import Foundation
import OSLog

public enum ScriptLaunch: Equatable, Sendable { case launched, busy, failed }

public final class ScriptProcessRunner: @unchecked Sendable {
    typealias Signal = @Sendable (pid_t, Int32) -> Int32
    typealias Launch = @Sendable (Process) throws -> Void
    typealias Pause = @Sendable (Duration) -> Void

    private static let log = Logger(subsystem: "com.ember.Ember", category: "controls")
    static let killGrace: Duration = .seconds(1)
    static let groupPoll: Duration = .milliseconds(50)
    static let lingerPoll: Duration = .seconds(1)
    static let shutdownPoll: Duration = .milliseconds(10)

    private struct Group {
        let leader: Process
        var watchdog: Task<Void, Never>?
        var escalation: Task<Void, Never>?
        var linger: Task<Void, Never>?

        func cancel() {
            watchdog?.cancel()
            escalation?.cancel()
            linger?.cancel()
        }
    }

    private let timeout: Duration
    private let signal: Signal
    private let launch: Launch
    private let pause: Pause
    private let state = NSCondition()
    private var running: Process?
    private var launching = false
    private var groups: [pid_t: Group] = [:]
    private var closed = false
    private var drained = false

    public convenience init(timeout: Duration = .seconds(10)) {
        self.init(timeout: timeout, signal: { kill($0, $1) })
    }

    init(timeout: Duration, signal: @escaping Signal, launch: @escaping Launch = { try $0.run() },
         pause: @escaping Pause = { Thread.sleep(forTimeInterval: ScriptProcessRunner.seconds($0)) }) {
        self.timeout = timeout
        self.signal = signal
        self.launch = launch
        self.pause = pause
    }

    public var isBusy: Bool { state.withLock { running != nil } }

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
        let claimed: Bool? = state.withLock {
            guard !closed else { return nil }
            guard running == nil else { return false }
            running = p
            launching = true
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
            try launch(p)
        } catch {
            state.withLock {
                launching = false
                if running === p { running = nil }
                state.broadcast()
            }
            Self.log.error("knob \(what, privacy: .public) did not start: \(error.localizedDescription, privacy: .public)")
            return .failed
        }
        let group = p.processIdentifier
        let (isClosed, isDrained): (Bool, Bool) = state.withLock {
            if !drained { groups[group] = Group(leader: p) }
            launching = false
            state.broadcast()
            return (closed, drained)
        }
        if isDrained { _ = deliver(SIGKILL, to: group, of: p) }
        if isClosed { return .failed }
        if let pipe, let stdin {
            let handle = pipe.fileHandleForWriting
            DispatchQueue.global(qos: .userInitiated).async {
                try? handle.write(contentsOf: stdin)
                try? handle.close()
            }
        }
        let limit = timeout
        let dog = Task.detached { [weak self] in
            guard (try? await Task.sleep(for: limit)) != nil, p.isRunning else { return }
            Self.log.notice("knob \(what, privacy: .public) killed after \(limit.components.seconds) s")
            self?.escalate(group, of: p)
        }
        state.withLock {
            if groups[group] != nil, running === p { groups[group]?.watchdog = dog } else { dog.cancel() }
        }
        return .launched
    }

    public func shutdown(grace: Duration = .milliseconds(500)) {
        let clock = ContinuousClock()
        let deadline = clock.now + grace
        let snapshot: [pid_t: Group] = state.withLock {
            closed = true
            let until = Date(timeIntervalSinceNow: Self.seconds(grace))
            while launching, state.wait(until: until) {}
            drained = true
            let taken = groups
            groups = [:]
            taken.values.forEach { $0.cancel() }
            return taken
        }
        var pending = snapshot.filter {
            !gone($0.key, $0.value.leader) && deliver(SIGTERM, to: $0.key, of: $0.value.leader)
        }
        while true {
            pending = pending.filter { !gone($0.key, $0.value.leader) }
            if pending.isEmpty || clock.now >= deadline { break }
            pause(min(Self.shutdownPoll, deadline - clock.now))
        }
        for (group, entry) in pending { _ = deliver(SIGKILL, to: group, of: entry.leader) }
    }

    private func escalate(_ group: pid_t, of p: Process) {
        guard deliver(SIGTERM, to: group, of: p) else { return }
        let task = Task.detached { [weak self] in
            let clock = ContinuousClock()
            let deadline = clock.now + Self.killGrace
            while clock.now < deadline {
                guard (try? await Task.sleep(for: Self.groupPoll)) != nil, let self else { return }
                if gone(group, p) {
                    forget(group)
                    return
                }
            }
            guard !Task.isCancelled, let self else { return }
            _ = deliver(SIGKILL, to: group, of: p)
            forget(group)
        }
        state.withLock {
            if groups[group] != nil { groups[group]?.escalation = task } else { task.cancel() }
        }
    }

    private func finished(_ p: Process) {
        state.withLock {
            while launching, running === p { state.wait() }
            if running === p { running = nil }
            let group = p.processIdentifier
            guard var entry = groups[group], entry.leader === p else { return }
            entry.watchdog?.cancel()
            entry.watchdog = nil
            if entry.escalation != nil {
                groups[group] = entry
            } else if signal(-group, 0) != 0 {
                groups[group] = nil
            } else {
                entry.linger = Task.detached { [weak self] in
                    while (try? await Task.sleep(for: Self.lingerPoll)) != nil, let self {
                        if signal(-group, 0) != 0 {
                            forget(group)
                            return
                        }
                    }
                }
                groups[group] = entry
            }
        }
    }

    private func forget(_ group: pid_t) {
        state.withLock { groups[group] = nil }
    }

    private func gone(_ group: pid_t, _ p: Process) -> Bool {
        signal(-group, 0) != 0 && !p.isRunning
    }

    private func deliver(_ sig: Int32, to group: pid_t, of p: Process) -> Bool {
        if signal(-group, sig) == 0 { return true }
        return p.isRunning && signal(group, sig) == 0
    }

    private static func seconds(_ d: Duration) -> TimeInterval {
        Double(d.components.seconds) + Double(d.components.attoseconds) / 1e18
    }
}
