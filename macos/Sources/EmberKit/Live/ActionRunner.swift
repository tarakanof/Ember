import Foundation
import Observation
import OSLog

public enum ClockAction: Hashable, Sendable {
    case next, previous, dismiss
    case power(Bool)
    case reboot
}

public enum EmberAction: Hashable, Sendable {
    case pomodoro(PomodoroAction)
    case setApp(String, enabled: Bool)
    case clock(ClockAction)

    var affectedFeeds: [Feed] {
        switch self {
        case .pomodoro: [.pomodoroState]
        case .setApp: [.apps]
        case .clock(.power): [.clockHealth]
        case .clock(.reboot): []
        case .clock: [.screen, .clockHealth]
        }
    }
}

@MainActor
@Observable
public final class ActionRunner {
    public struct Failure: Equatable, Sendable {
        public let action: EmberAction
        public let error: FeedError
        public let at: Date
    }

    public private(set) var lastError: Failure?
    public private(set) var running: Set<EmberAction> = []
    public var pendingDisplayPower: Bool? {
        if running.contains(.clock(.power(true))) { return true }
        if running.contains(.clock(.power(false))) { return false }
        return nil
    }

    @ObservationIgnored private let live: LiveModel
    @ObservationIgnored private let connection: ServerConnection
    @ObservationIgnored private let clearAfter: Duration
    @ObservationIgnored private let now: @MainActor () -> Date
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var clearTask: Task<Void, Never>?

    private static let log = Logger(subsystem: "com.ember.Ember", category: "actions")

    public convenience init(live: LiveModel, connection: ServerConnection) {
        self.init(live: live, connection: connection, clearAfter: .seconds(10), now: { Date() },
                  sleep: { try await Task.sleep(for: $0) })
    }

    init(live: LiveModel, connection: ServerConnection, clearAfter: Duration,
         now: @escaping @MainActor () -> Date,
         sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.live = live
        self.connection = connection
        self.clearAfter = clearAfter
        self.now = now
        self.sleep = sleep
    }

    private static func perform(_ action: EmberAction, on connection: ServerConnection) async throws {
        let client = connection.client
        let device = connection.device
        switch action {
        case .pomodoro(let a): try await client.send("POST", "/v1/pomodoro/\(a.rawValue)")
        case .setApp(let name, let enabled): try await client.put("/v1/apps", body: SetAppRequest(app: name, enabled: enabled))
        case .clock(.next): try await device.nextApp()
        case .clock(.previous): try await device.previousApp()
        case .clock(.dismiss): try await device.dismiss()
        case .clock(.power(let on)): try await device.setDisplayPower(on)
        case .clock(.reboot): try await device.reboot()
        }
    }

    @discardableResult
    public func run(_ action: EmberAction) async -> Bool {
        running.insert(action)
        defer { running.remove(action) }
        var ok = false
        let ticket = live.displayPowerTicket()
        do {
            try await Self.perform(action, on: connection)
            ok = true
            if lastError?.action == action { lastError = nil }
            switch action {
            case .clock(.power(let on)): live.reportDisplayPower(on, written: ticket)
            case .clock(.reboot): live.reportDisplayPower(true, written: ticket)
            default: break
            }
        } catch {
            let e = FeedError(error)
            Self.log.info("action failed: \(String(describing: action), privacy: .public) \(e.localizedDescription, privacy: .public)")
            record(Failure(action: action, error: e, at: now()))
        }
        let feeds = action.affectedFeeds
        if !feeds.isEmpty { await live.refreshNow(feeds, ifOlderThan: .zero) }
        return ok
    }

    private func record(_ failure: Failure) {
        lastError = failure
        clearTask?.cancel()
        let wait = clearAfter
        let sleep = self.sleep
        clearTask = Task { [weak self] in
            do { try await sleep(wait) } catch { return }
            guard let self, self.lastError == failure else { return }
            self.lastError = nil
        }
    }
}

private struct SetAppRequest: Encodable { let app: String; let enabled: Bool }
