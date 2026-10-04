import Foundation

/// A display as the Settings sidebar needs it.
public struct SettingsDevice: Equatable, Sendable {
    /// Whether the device's node can show its pages yet.
    public enum State: Equatable, Sendable {
        /// The device list hasn't loaded: a stored route under it is held.
        case loading
        /// Nothing registered: the node shows the setup state, no children.
        case notSetUp
        case ready
    }

    public var id: String
    public var kind: DeviceKind
    public var name: String
    public var state: State
    /// Page ids the firmware reports drawing; nil when it doesn't say.
    public var supportedPages: [String]?

    public init(id: String, kind: DeviceKind, name: String, state: State, supportedPages: [String]? = nil) {
        self.id = id
        self.kind = kind
        self.name = name
        self.state = state
        self.supportedPages = supportedPages
    }
}

/// The Settings sidebar: App and Sources panes, then one node per device
/// with its hardware pages and an Apps subtree.
public struct SettingsTree: Equatable, Sendable {
    /// One device node and its children.
    public struct Device: Identifiable, Equatable, Sendable {
        public let device: SettingsDevice
        /// Hardware pages; empty unless the device is ready.
        public let hardware: [SettingsRoute]
        /// The device's apps; empty unless the device is ready.
        public let apps: [SettingsRoute]

        public var id: String { device.id }
        /// The route selecting the node itself opens: the device's Status.
        public var route: SettingsRoute { .device(device.id, .hardware(.status)) }
        /// The Apps node's own route (Rotation / Pages), when it has one.
        public var appsRoute: SettingsRoute? { device.state == .ready ? .device(device.id, .apps) : nil }
        /// Expansion ids of the device node and its Apps node.
        public var expansionID: String { device.id }
        public var appsExpansionID: String { device.id + "/apps" }
    }

    public let app: [SettingsRoute] = AppPane.allCases.map(SettingsRoute.app)
    public let sources: [SettingsRoute] = SourceID.allCases.map(SettingsRoute.source)
    public let devices: [Device]

    public init(devices: [SettingsDevice]) {
        self.devices = devices.map { d in
            guard d.state == .ready else { return Device(device: d, hardware: [], apps: []) }
            return Device(device: d,
                          hardware: AppCatalog.hardware(d.kind).dropFirst().map { .device(d.id, .hardware($0)) },
                          apps: AppCatalog.apps(d.kind, supportedPages: d.supportedPages).map { .device(d.id, .app($0)) })
        }
    }

    /// The route the sidebar can show for a stored one: a missing device
    /// becomes the first device of its kind, a page the device lacks its
    /// Status (or its Apps page, for an app), and a route under a device
    /// still loading is kept as is.
    public func resolve(_ route: SettingsRoute) -> SettingsRoute {
        guard case .device(let id, let page) = route else { return route }
        let node = devices.first { $0.id == id }
            ?? DeviceKind(deviceID: id).flatMap { kind in devices.first { $0.device.kind == kind } }
        guard let node else { return SettingsRoute.fallback }
        switch node.device.state {
        case .loading:
            return route
        case .notSetUp:
            return node.route
        case .ready:
            let moved = SettingsRoute.device(node.id, page)
            switch page {
            case .hardware(.status), .apps: return moved
            case .hardware: return node.hardware.contains(moved) ? moved : node.route
            case .app: return node.apps.contains(moved) ? moved : .device(node.id, .apps)
            }
        }
    }

    /// The nodes to expand so `route` is visible in the sidebar.
    public func expansionIDs(revealing route: SettingsRoute) -> [String] {
        guard case .device(let id, let page) = route, let node = devices.first(where: { $0.id == id }) else { return [] }
        switch page {
        case .hardware(.status): return []
        case .hardware, .apps: return [node.expansionID]
        case .app: return [node.expansionID, node.appsExpansionID]
        }
    }

    /// Every device app presenting `source`, in sidebar order: the
    /// Sources pane's "Shown on" links.
    public func apps(showing source: SourceID) -> [(device: SettingsDevice, route: SettingsRoute)] {
        devices.flatMap { node in
            node.apps.compactMap { route -> (device: SettingsDevice, route: SettingsRoute)? in
                guard case .device(_, .app(let app)) = route, AppCatalog.source(of: app) == source else { return nil }
                return (node.device, route)
            }
        }
    }

    /// The expansion set as stored (comma-joined ids).
    public static func expandedSet(_ stored: String) -> Set<String> {
        Set(stored.split(separator: ",").map(String.init))
    }

    /// The stored form of an expansion set, sorted so it doesn't churn.
    public static func storedExpanded(_ set: Set<String>) -> String {
        set.sorted().joined(separator: ",")
    }
}
