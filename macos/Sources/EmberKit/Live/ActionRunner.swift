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
        case .pomodoro: [.pomodoroState, .stats]
        case .setApp: [.apps]
        case .clock(.power): [.clockHealth]
        case .clock(.reboot): []
        case .clock: [.screen, .clockHealth]
        }
    }
}

/// Runs user actions for the menu, the Dashboard and the Dock menu, so none of
/// them swallows a failure with `try?`. The last failure stays in `lastError`
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

    @ObservationIgnored private let live: LiveModel
    @ObservationIgnored private let clearAfter: Duration
    @ObservationIgnored private let now: @MainActor () -> Date
    @ObservationIgnored private var perform: (@Sendable (EmberAction) async throws -> Void)?
    @ObservationIgnored private var clearTask: Task<Void, Never>?

    private static let log = Logger(subsystem: "com.ember.Ember", category: "actions")

    public init(live: LiveModel, clearAfter: Duration = .seconds(10),
                now: @escaping @MainActor () -> Date = { Date() }) {
        self.live = live
        self.clearAfter = clearAfter
        self.now = now
    }

    /// Points actions at a new server (alongside `LiveModel.configure`).
    public func configure(client: APIClient) {
        let pomodoro = PomodoroService(client: client)
        let apps = AppsService(client: client)
        let device = DeviceService(client: client)
        perform = { action in
            switch action {
            case .pomodoro(let a): try await pomodoro.action(a)
            case .setApp(let name, let enabled): try await apps.set(name, enabled: enabled)
            case .clock(.next): try await device.nextApp()
            case .clock(.previous): try await device.previousApp()
            case .clock(.dismiss): try await device.dismiss()
            case .clock(.power(let on)): try await device.setDisplayPower(on)
            case .clock(.reboot): try await device.reboot()
            }
        }
    }

    /// Runs the action, records a failure, then refreshes the feeds it
    /// touched. Returns whether it succeeded.
    @discardableResult
    public func run(_ action: EmberAction) async -> Bool {
        running.insert(action)
        defer { running.remove(action) }
        var ok = false
        do {
            guard let perform else { throw FeedError.offline }
            try await perform(action)
            ok = true
            if lastError?.action == action { lastError = nil }
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
        clearTask = Task { [weak self] in
            try? await Task.sleep(for: wait)
            guard !Task.isCancelled, let self, self.lastError == failure else { return }
            self.lastError = nil
        }
    }
}
