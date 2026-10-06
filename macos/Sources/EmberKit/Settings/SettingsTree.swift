import Foundation

public struct SettingsDevice: Equatable, Sendable {
    public enum State: Equatable, Sendable {
        case loading
        case unavailable
        case notSetUp
        case ready
    }

    public var id: String
    public var kind: DeviceKind
    public var name: String
    public var state: State
    public var supportedPages: [String]?

    public init(id: String, kind: DeviceKind, name: String, state: State, supportedPages: [String]? = nil) {
        self.id = id
        self.kind = kind
        self.name = name
        self.state = state
        self.supportedPages = supportedPages
    }
}

public struct SettingsTree: Equatable, Sendable {
    public struct Device: Identifiable, Equatable, Sendable {
        public let device: SettingsDevice
        public let hardware: [SettingsRoute]
        public let apps: [SettingsRoute]

        public var id: String { device.id }
        public var route: SettingsRoute { .device(device.id, .hardware(.status)) }
        public var appsRoute: SettingsRoute? { device.state == .ready ? .device(device.id, .apps) : nil }
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

    public func resolve(_ route: SettingsRoute) -> SettingsRoute {
        guard case .device(let id, let page) = route else { return route }
        let node = devices.first { $0.id == id }
            ?? DeviceKind(deviceID: id).flatMap { kind in devices.first { $0.device.kind == kind } }
        guard let node else { return SettingsRoute.fallback }
        switch node.device.state {
        case .loading, .unavailable:
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

    public var holdsStoredRoute: Bool {
        devices.contains { $0.device.state == .loading || $0.device.state == .unavailable }
    }

    public func expanded(_ expanded: Set<String>, revealing route: SettingsRoute) -> Set<String> {
        expanded.union(expansionIDs(revealing: route))
    }

    public func expansionIDs(revealing route: SettingsRoute) -> [String] {
        guard case .device(let id, let page) = route, let node = devices.first(where: { $0.id == id }) else { return [] }
        switch page {
        case .hardware(.status): return []
        case .hardware, .apps: return [node.expansionID]
        case .app: return [node.expansionID, node.appsExpansionID]
        }
    }

    public func apps(showing source: SourceID) -> [(device: SettingsDevice, route: SettingsRoute)] {
        devices.flatMap { node in
            node.apps.compactMap { route -> (device: SettingsDevice, route: SettingsRoute)? in
                guard case .device(_, .app(let app)) = route, AppCatalog.source(of: app) == source else { return nil }
                return (node.device, route)
            }
        }
    }

    public static func expandedSet(_ stored: String) -> Set<String> {
        Set(stored.split(separator: ",").map(String.init))
    }

    public static func storedExpanded(_ set: Set<String>) -> String {
        set.sorted().joined(separator: ",")
    }
}
