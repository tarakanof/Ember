import Foundation
import Observation

/// The OS permissions Ember depends on, in the order Settings › Permissions
/// lists them.
public enum PermissionID: String, CaseIterable, Identifiable, Sendable {
    /// This app reaching the server and clock on the LAN.
    case localNetwork
    /// The producer helpers reaching the server (their own Local Network grant).
    case helperLocalNetwork
    /// The producer LaunchAgents (System Settings › General › Login Items).
    case backgroundItems
    /// Apple Reminders, for reminder alarms on the clock.
    case reminders
    /// Location, for Weather's Detect button.
    case location
    /// Automation of Music.app (Apple Events), for the Apple Music pusher.
    case musicAutomation

    public var id: String { rawValue }
}

/// A permission's state as Settings shows it.
public enum PermissionStatus: Equatable, Sendable {
    /// Not read yet.
    case checking
    case granted
    case denied
    /// macOS hasn't asked yet.
    case notDetermined
    /// Background items: waiting for approval in Login Items.
    case needsApproval
    /// Background items: registered, but launchd isn't running the helper.
    case notRunning
    /// The probe couldn't tell (no network, nothing answered).
    case unknown
    /// The feature that needs it is off, so it doesn't matter.
    case notInUse
}

/// A privacy/authorization status, as read from EventKit or CoreLocation by
/// the app and handed to the model without those frameworks.
public enum AccessStatus: Equatable, Sendable {
    case granted, denied, notDetermined
}

/// What a row's button does.
public enum PermissionAction: Equatable, Sendable {
    /// Opens a System Settings pane.
    case openSystemSettings(SystemSettingsPane)
    /// Shows macOS's prompt (only possible while `.notDetermined`).
    case requestAccess
    /// Re-registers the producer helpers (`ProducerInstallModel.repair()`).
    case repair
    /// Opens another Settings pane.
    case openPane(SettingsRoute)
    /// Shows macOS's Automation prompt for Music.app.
    case requestMusicAutomation
}

/// System Settings panes the Permissions pane links to.
public enum SystemSettingsPane: String, Sendable {
    case localNetwork = "x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork"
    case reminders = "x-apple.systempreferences:com.apple.preference.security?Privacy_Reminders"
    case location = "x-apple.systempreferences:com.apple.preference.security?Privacy_LocationServices"
    case loginItems = "x-apple.systempreferences:com.apple.LoginItems-Settings.extension"
    case automation = "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation"

    public var url: URL { URL(string: rawValue)! }
}

/// One row of Settings › Permissions.
public struct PermissionRow: Identifiable, Equatable, Sendable {
    public let id: PermissionID
    public let status: PermissionStatus
    /// Whether something Ember is set to do needs it right now.
    public let required: Bool
    public let action: PermissionAction?
    /// Helpers macOS blocks from the LAN (`helperLocalNetwork` only).
    public let blockedHelpers: [ProducerAgent]

    public init(id: PermissionID, status: PermissionStatus, required: Bool, action: PermissionAction?,
                blockedHelpers: [ProducerAgent] = []) {
        self.id = id
        self.status = status
        self.required = required
        self.action = action
        self.blockedHelpers = blockedHelpers
    }

    /// A required permission that is off, or waiting on the user: what the
    /// General pane's warning counts.
    public var needsAttention: Bool {
        guard required else { return false }
        switch status {
        case .denied, .notDetermined, .needsApproval, .notRunning: return true
        case .checking, .granted, .unknown, .notInUse: return false
        }
    }
}

/// The reads behind the Permissions pane.
@MainActor
public protocol PermissionSources: AnyObject, Sendable {
    /// Probes this app's Local Network access (a few seconds at most).
    func localNetwork() async -> LocalNetworkStatus
    /// The producer agents' state; nil when it can't be read.
    func producers() async -> ProducerSnapshot?
    /// Reminders access, and whether reminder alarms are on.
    func reminders() -> (status: AccessStatus, inUse: Bool)
    func location() -> AccessStatus
    /// Whether Ember may send Apple Events to Music.app (nil when it can't
    /// tell, e.g. Music isn't running), and whether the pusher is on.
    func musicAutomation() async -> (status: AccessStatus?, inUse: Bool)
}

/// Live status of every permission Ember uses, re-read on demand (the pane
/// appearing, the app becoming active, after a fix).
@MainActor
@Observable
public final class PermissionsModel {
    public private(set) var rows: [PermissionRow]
    public private(set) var isChecking = false
    /// When the last full read finished.
    public private(set) var checkedAt: Date?

    /// How soon after one check the pane appearing or the app becoming
    /// active checks again.
    public static let activationInterval: TimeInterval = 5

    @ObservationIgnored private let sources: any PermissionSources
    @ObservationIgnored private let now: @MainActor () -> Date
    @ObservationIgnored private var inFlight: Task<Void, Never>?
    @ObservationIgnored private var startedAt: Date?

    public init(sources: any PermissionSources, now: @escaping @MainActor () -> Date = { Date() }) {
        self.sources = sources
        self.now = now
        rows = PermissionID.allCases.map {
            PermissionRow(id: $0, status: .checking, required: false, action: nil)
        }
    }

    /// Required permissions that are off.
    public var attention: [PermissionRow] { rows.filter(\.needsAttention) }

    public func row(_ id: PermissionID) -> PermissionRow? { rows.first { $0.id == id } }

    /// Re-reads everything.
    public func refresh(ifOlderThan minInterval: TimeInterval? = nil) async {
        if let running = inFlight {
            await running.value
            guard minInterval == nil else { return }
            if let next = inFlight {
                await next.value
                return
            }
        }
        if let minInterval, let startedAt, now().timeIntervalSince(startedAt) < minInterval { return }
        startedAt = now()
        let task = Task {
            await check()
            inFlight = nil
        }
        inFlight = task
        await task.value
    }

    private func check() async {
        isChecking = true
        let reminders = sources.reminders()
        let location = sources.location()
        let music = await sources.musicAutomation()
        let previous = Dictionary(uniqueKeysWithValues: rows.map { ($0.id, $0) })
        let lastNetwork = previous[.localNetwork].flatMap { Self.networkStatus(of: $0.status) }
        rows = Self.rows(localNetwork: lastNetwork, producers: nil, keepingProducerRowsFrom: previous,
                         reminders: reminders.status, remindersInUse: reminders.inUse, location: location,
                         music: music.status, musicInUse: music.inUse)

        async let network = sources.localNetwork()
        async let producers = sources.producers()
        let (n, p) = await (network, producers)
        rows = Self.rows(localNetwork: n, producers: p, keepingProducerRowsFrom: previous,
                         reminders: reminders.status, remindersInUse: reminders.inUse, location: location,
                         music: music.status, musicInUse: music.inUse)
        isChecking = false
        checkedAt = Date()
    }

    nonisolated private static func networkStatus(of status: PermissionStatus) -> LocalNetworkStatus? {
        switch status {
        case .granted: .granted
        case .denied: .denied
        case .unknown: .unknown
        default: nil
        }
    }

    // MARK: Rules (pure)

    nonisolated static func rows(localNetwork: LocalNetworkStatus?, producers: ProducerSnapshot?,
                     keepingProducerRowsFrom previous: [PermissionID: PermissionRow] = [:],
                     reminders: AccessStatus, remindersInUse: Bool,
                     location: AccessStatus,
                     music: AccessStatus? = nil, musicInUse: Bool = false) -> [PermissionRow] {
        var byID: [PermissionID: PermissionRow] = [
            .localNetwork: localNetworkRow(localNetwork),
            .reminders: remindersRow(reminders, inUse: remindersInUse),
            .location: locationRow(location),
            .musicAutomation: musicAutomationRow(music, inUse: musicInUse),
        ]
        for id in [PermissionID.backgroundItems, .helperLocalNetwork] {
            byID[id] = previous[id] ?? PermissionRow(id: id, status: .checking, required: false, action: nil)
        }
        if let producers {
            byID[.backgroundItems] = backgroundItemsRow(producers)
            byID[.helperLocalNetwork] = helperLocalNetworkRow(producers)
        }
        return PermissionID.allCases.compactMap { byID[$0] }
    }

    nonisolated static func localNetworkRow(_ status: LocalNetworkStatus?) -> PermissionRow {
        let s: PermissionStatus = switch status {
        case nil: .checking
        case .granted?: .granted
        case .denied?: .denied
        case .unknown?: .unknown
        }
        return PermissionRow(id: .localNetwork, status: s, required: true,
                             action: .openSystemSettings(.localNetwork))
    }

    nonisolated static func backgroundItemsRow(_ snapshot: ProducerSnapshot) -> PermissionRow {
        let states = snapshot.agents.map(\.state)
        let inUse = states.contains { $0 != .off }
        let status: PermissionStatus
        let action: PermissionAction
        if !inUse {
            status = .notInUse
            action = .openPane(.source(.agents))
        } else if states.contains(.needsApproval) {
            status = .needsApproval
            action = .openSystemSettings(.loginItems)
        } else if states.contains(.notRunning) {
            status = .notRunning
            action = .repair
        } else if states.contains(where: { if case .error = $0 { true } else { false } }) {
            status = .unknown
            action = .openPane(.source(.agents))
        } else {
            status = .granted
            action = .openPane(.source(.agents))
        }
        return PermissionRow(id: .backgroundItems, status: status, required: inUse, action: action)
    }

    nonisolated static func helperLocalNetworkRow(_ snapshot: ProducerSnapshot) -> PermissionRow {
        let running = snapshot.agents.contains { $0.state == .on }
        guard running else {
            return PermissionRow(id: .helperLocalNetwork, status: .notInUse, required: false,
                                 action: .openSystemSettings(.localNetwork))
        }
        let blocked = snapshot.localNetworkBlocked
        return PermissionRow(id: .helperLocalNetwork, status: blocked.isEmpty ? .granted : .denied,
                             required: true, action: .openSystemSettings(.localNetwork),
                             blockedHelpers: blocked)
    }

    nonisolated static func remindersRow(_ access: AccessStatus, inUse: Bool) -> PermissionRow {
        guard inUse else {
            return PermissionRow(id: .reminders, status: .notInUse, required: false, action: .openPane(.source(.calendar)))
        }
        return PermissionRow(id: .reminders, status: status(access), required: true,
                             action: access == .notDetermined ? .requestAccess : .openSystemSettings(.reminders))
    }

    nonisolated static func locationRow(_ access: AccessStatus) -> PermissionRow {
        PermissionRow(id: .location, status: status(access), required: false,
                      action: access == .notDetermined ? .openPane(.source(.weather)) : .openSystemSettings(.location))
    }

    nonisolated static func musicAutomationRow(_ access: AccessStatus?, inUse: Bool) -> PermissionRow {
        guard inUse else {
            return PermissionRow(id: .musicAutomation, status: .notInUse, required: false,
                                 action: .openPane(.source(.music)))
        }
        guard let access else {
            return PermissionRow(id: .musicAutomation, status: .unknown, required: true,
                                 action: .openSystemSettings(.automation))
        }
        return PermissionRow(id: .musicAutomation, status: status(access), required: true,
                             action: access == .notDetermined ? .requestMusicAutomation : .openSystemSettings(.automation))
    }

    nonisolated private static func status(_ access: AccessStatus) -> PermissionStatus {
        switch access {
        case .granted: .granted
        case .denied: .denied
        case .notDetermined: .notDetermined
        }
    }
}
