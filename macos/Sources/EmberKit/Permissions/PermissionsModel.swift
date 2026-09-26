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
    case openPane(SettingsPaneID)
}

/// System Settings panes the Permissions pane links to.
public enum SystemSettingsPane: String, Sendable {
    case localNetwork = "x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork"
    case reminders = "x-apple.systempreferences:com.apple.preference.security?Privacy_Reminders"
    case location = "x-apple.systempreferences:com.apple.preference.security?Privacy_LocationServices"
    case loginItems = "x-apple.systempreferences:com.apple.LoginItems-Settings.extension"

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

/// The reads behind the Permissions pane. The app implements them over
/// EventKit, CoreLocation, `ProducerInstallService` and `LocalNetworkProbe`;
/// tests fake them.
@MainActor
public protocol PermissionSources: AnyObject, Sendable {
    /// Probes this app's Local Network access (a few seconds at most).
    func localNetwork() async -> LocalNetworkStatus
    /// The producer agents' state; nil when it can't be read.
    func producers() async -> ProducerSnapshot?
    /// Reminders access, and whether reminder alarms are on.
    func reminders() -> (status: AccessStatus, inUse: Bool)
    func location() -> AccessStatus
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

    @ObservationIgnored private let sources: any PermissionSources
    @ObservationIgnored private var seq = 0

    public init(sources: any PermissionSources) {
        self.sources = sources
        rows = PermissionID.allCases.map {
            PermissionRow(id: $0, status: .checking, required: false, action: nil)
        }
    }

    /// Required permissions that are off.
    public var attention: [PermissionRow] { rows.filter(\.needsAttention) }

    public func row(_ id: PermissionID) -> PermissionRow? { rows.first { $0.id == id } }

    /// Re-reads everything. The quick reads land at once; the Local Network
    /// probe and the producer read follow. A read applies only if no newer
    /// refresh started.
    public func refresh() async {
        seq += 1
        let mine = seq
        isChecking = true
        let reminders = sources.reminders()
        let location = sources.location()
        let previous = Dictionary(uniqueKeysWithValues: rows.map { ($0.id, $0) })
        // Keep what the slow reads showed last time until they answer again.
        let lastNetwork = previous[.localNetwork].flatMap { Self.networkStatus(of: $0.status) }
        rows = Self.rows(localNetwork: lastNetwork, producers: nil, keepingProducerRowsFrom: previous,
                         reminders: reminders.status, remindersInUse: reminders.inUse, location: location)

        async let network = sources.localNetwork()
        async let producers = sources.producers()
        let (n, p) = await (network, producers)
        guard mine == seq else { return }
        rows = Self.rows(localNetwork: n, producers: p, keepingProducerRowsFrom: previous,
                         reminders: reminders.status, remindersInUse: reminders.inUse, location: location)
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

    /// The rows for a set of reads. nil `localNetwork` or `producers` means
    /// "not read yet": the Local Network row shows `.checking`, the producer
    /// rows keep `keepingProducerRowsFrom`'s (or `.checking`).
    nonisolated static func rows(localNetwork: LocalNetworkStatus?, producers: ProducerSnapshot?,
                     keepingProducerRowsFrom previous: [PermissionID: PermissionRow] = [:],
                     reminders: AccessStatus, remindersInUse: Bool,
                     location: AccessStatus) -> [PermissionRow] {
        var byID: [PermissionID: PermissionRow] = [
            .localNetwork: localNetworkRow(localNetwork),
            .reminders: remindersRow(reminders, inUse: remindersInUse),
            .location: locationRow(location),
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

    /// The producer LaunchAgents: not in use unless reporting is on for some
    /// agent; approval or Repair when macOS isn't running one.
    nonisolated static func backgroundItemsRow(_ snapshot: ProducerSnapshot) -> PermissionRow {
        let states = snapshot.agents.map(\.state)
        let inUse = states.contains { $0 != .off }
        let status: PermissionStatus
        let action: PermissionAction
        if !inUse {
            status = .notInUse
            action = .openPane(.agents)
        } else if states.contains(.needsApproval) {
            status = .needsApproval
            action = .openSystemSettings(.loginItems)
        } else if states.contains(.notRunning) {
            status = .notRunning
            action = .repair
        } else if states.contains(where: { if case .error = $0 { true } else { false } }) {
            status = .unknown
            action = .openPane(.agents)
        } else {
            status = .granted
            action = .openPane(.agents)
        }
        return PermissionRow(id: .backgroundItems, status: status, required: inUse, action: action)
    }

    /// The helpers' own Local Network grant, from what each running helper
    /// last recorded (`no_route`).
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
        PermissionRow(id: .reminders, status: status(access), required: inUse,
                      action: access == .notDetermined ? .requestAccess : .openSystemSettings(.reminders))
    }

    /// Location is only asked for by Weather's Detect button, so it's never
    /// required, and "not asked yet" sends the user there.
    nonisolated static func locationRow(_ access: AccessStatus) -> PermissionRow {
        PermissionRow(id: .location, status: status(access), required: false,
                      action: access == .notDetermined ? .openPane(.weather) : .openSystemSettings(.location))
    }

    nonisolated private static func status(_ access: AccessStatus) -> PermissionStatus {
        switch access {
        case .granted: .granted
        case .denied: .denied
        case .notDetermined: .notDetermined
        }
    }
}
