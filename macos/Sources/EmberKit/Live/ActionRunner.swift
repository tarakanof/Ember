import Foundation
import Observation
import OSLog

/// A clock control.
public enum ClockAction: Hashable, Sendable {
    case next, previous, dismiss
    /// Blank (false) or relight (true) the matrix.
    case power(Bool)
    case reboot
}

/// Something the user asked Ember to do.
public enum EmberAction: Hashable, Sendable {
    case pomodoro(PomodoroAction)
    /// Show or hide an Ember app on the clock (`/v1/apps`).
    case setApp(String, enabled: Bool)
    case clock(ClockAction)

    /// The feeds whose values the action changes.
    var affectedFeeds: [Feed] {
        switch self {
        // Stats follow from the timer: LiveModel refreshes them when the
        // phase changes, so they aren't fetched twice per action.
        case .pomodoro: [.pomodoroState]
        case .setApp: [.apps]
        case .clock(.power): [.clockHealth]
        case .clock(.reboot): []
        case .clock: [.screen, .clockHealth]
        }
    }
}

/// Runs user actions for the menu, the Dashboard, the Dock menu and Settings
/// (the one path for clock actions), so none of them swallows a failure with
/// `try?`. A display-power write or a reboot reports the new matrix state to
/// `LiveModel.displayPower`. The last failure stays in `lastError`
/// for 10 s, for a transient "Couldn't start: Unauthorized" row.
@MainActor
@Observable
public final class ActionRunner {
    public struct Failure: Equatable, Sendable {
        public let action: EmberAction
        public let error: FeedError
        public let at: Date
    }

    /// The most recent failure; cleared by the next success or after
    /// `clearAfter`.
    public private(set) var lastError: Failure?
    /// Actions currently running, so a view can disable a button meanwhile.
    public private(set) var running: Set<EmberAction> = []
    /// The state an in-flight display write is setting, nil when none is.
    /// Switches show it at once (`pendingDisplayPower ?? live.displayPower`);
    /// a failure simply ends it, and `displayPower` never moved.
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

    /// Actions go to whichever server `connection` holds when they run.
    public convenience init(live: LiveModel, connection: ServerConnection) {
        self.init(live: live, connection: connection, clearAfter: .seconds(10), now: { Date() },
                  sleep: { try await Task.sleep(for: $0) })
    }

    /// Tests inject the clocks.
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

    /// Runs the action, records a failure, then refreshes the feeds it
    /// touched. Returns whether it succeeded.
    @discardableResult
    public func run(_ action: EmberAction) async -> Bool {
        running.insert(action)
        defer { running.remove(action) }
        var ok = false
        // Taken before the request: a write that returns after a reconnect
        // says nothing about the new server's clock.
        let ticket = live.displayPowerTicket()
        do {
            try await Self.perform(action, on: connection)
            ok = true
            if lastError?.action == action { lastError = nil }
            switch action {
            case .clock(.power(let on)): live.reportDisplayPower(on, written: ticket)
            // A reboot relights the matrix.
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
