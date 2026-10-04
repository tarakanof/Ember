import SwiftUI
import EmberKit

struct SettingsRootView: View {
    @Environment(AppEnvironment.self) private var env
    @AppStorage(SettingsRoute.storageKey) private var routeName = SettingsRoute.fallback.stored
    @AppStorage(SettingsRoute.expandedKey) private var expandedName = ""

    static let windowWidth: CGFloat = 800

    private var tree: SettingsTree { SettingsTree(devices: devices) }

    private var devices: [SettingsDevice] {
        let knob = env.knob
        let knobState: SettingsDevice.State = if knob.knob != nil {
            .ready
        } else if !knob.isLoaded && knob.loadError == nil {
            .loading
        } else {
            .notSetUp
        }
        return [
            SettingsDevice(id: clockDeviceID, kind: .clock, name: String(localized: DeviceKind.clock.title), state: .ready),
            SettingsDevice(id: knob.knob?.id ?? DeviceKind.knob.placeholderID, kind: .knob,
                           name: knob.knob?.name ?? String(localized: DeviceKind.knob.title), state: knobState),
        ]
    }

    private var selection: Binding<SettingsRoute?> {
        Binding(
            get: { tree.resolve(SettingsRoute(stored: routeName)) },
            set: { if let route = $0 { routeName = route.stored } })
    }

    var body: some View {
        let tree = tree
        let route = tree.resolve(SettingsRoute(stored: routeName))
        NavigationSplitView {
            List(selection: selection) {
                Section("App") { ForEach(tree.app, id: \.self) { row($0, in: tree) } }
                Section("Sources") { ForEach(tree.sources, id: \.self) { row($0, in: tree) } }
                Section("Devices") { ForEach(tree.devices) { deviceNode($0, in: tree) } }
            }
            .navigationSplitViewColumnWidth(min: 190, ideal: 220, max: 250)
        } detail: {
            SettingsDetail(route: route, tree: tree)
                .environment(env.deviceSettings)
                .environment(\.settingsTree, tree)
                .navigationTitle(tree.windowTitle(for: route))
                .navigationSubtitle(subtitle(env.deviceSettings))
                .frame(minWidth: 460, minHeight: 360)
        }
        .task { await env.knob.load() }
        .onAppear { settle(tree) }
        .onChange(of: routeName) { settle(tree) }
        .onChange(of: tree) { _, new in settle(new) }
    }

    /// Opens the selection's ancestors and, once no device is loading,
    /// stores the route the sidebar actually shows.
    private func settle(_ tree: SettingsTree) {
        let route = tree.resolve(SettingsRoute(stored: routeName))
        var expanded = SettingsTree.expandedSet(expandedName)
        expanded.formUnion(tree.expansionIDs(revealing: route))
        let stored = SettingsTree.storedExpanded(expanded)
        if stored != expandedName { expandedName = stored }
        let loading = tree.devices.contains { $0.device.state == .loading }
        if !loading, route.stored != routeName { routeName = route.stored }
    }

    private func isExpanded(_ id: String) -> Binding<Bool> {
        Binding(
            get: { SettingsTree.expandedSet(expandedName).contains(id) },
            set: { open in
                var set = SettingsTree.expandedSet(expandedName)
                if open { set.insert(id) } else { set.remove(id) }
                expandedName = SettingsTree.storedExpanded(set)
            })
    }

    private func row(_ route: SettingsRoute, in tree: SettingsTree) -> some View {
        Label { Text(tree.title(for: route)) } icon: { Image(systemName: tree.systemImage(for: route)) }
            .tag(route)
            .accessibilityLabel(accessibilityLabel(route, in: tree))
    }

    private func accessibilityLabel(_ route: SettingsRoute, in tree: SettingsTree) -> Text {
        guard let node = tree.node(for: route) else { return Text(tree.title(for: route)) }
        return Text("\(String(localized: tree.title(for: route))) on \(node.device.name)",
                    comment: "VoiceOver label of a Settings sidebar row under a device: the page (\"Weather\"), then the device's name (\"Clock\").")
    }

    @ViewBuilder
    private func deviceNode(_ node: SettingsTree.Device, in tree: SettingsTree) -> some View {
        switch node.device.state {
        case .loading:
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text("\(String(localized: node.device.kind.title)) loading…",
                     comment: "Settings sidebar: a device's row while the device list loads (\"Knob loading…\").")
                    .foregroundStyle(.secondary)
            }
        case .notSetUp:
            DeviceRow(device: node.device).tag(node.route)
        case .ready:
            DisclosureGroup(isExpanded: isExpanded(node.expansionID)) {
                ForEach(node.hardware, id: \.self) { row($0, in: tree) }
                DisclosureGroup(isExpanded: isExpanded(node.appsExpansionID)) {
                    ForEach(node.apps, id: \.self) { row($0, in: tree) }
                } label: {
                    if let apps = node.appsRoute {
                        Label { Text("Apps") } icon: { Image(systemName: "square.grid.2x2") }
                            .tag(apps)
                            .accessibilityLabel(Text("Apps, \(node.device.name)",
                                                     comment: "VoiceOver label of a device's Apps row in the Settings sidebar; the argument is the device's name (\"Clock\")."))
                            .accessibilityHint(Text(node.device.kind.appsTitle))
                    }
                }
            } label: {
                DeviceRow(device: node.device).tag(node.route)
            }
        }
    }

    private func subtitle(_ device: DeviceSettingsModel) -> Text {
        let status = AggregateSaveStatus.combine((env.settings.all + device.all + env.knob.all).map(\.status))
        return status.subtitle.map { Text($0) } ?? Text(verbatim: "")
    }
}

/// A device's sidebar row: its name, kind and whether it's reachable.
private struct DeviceRow: View {
    @Environment(AppEnvironment.self) private var env
    let device: SettingsDevice

    private var online: Bool? {
        switch device.kind {
        case .clock:
            return env.live.clockHealth.value?.device?.reachable
        case .knob:
            guard device.state == .ready, let knob = env.knob.knob else { return nil }
            return knob.isOnline(now: .now)
        }
    }

    var body: some View {
        Label {
            HStack(spacing: 6) {
                Text(verbatim: device.name)
                Spacer(minLength: 4)
                if let online {
                    Image(systemName: "circle.fill")
                        .font(.system(size: 7))
                        .foregroundStyle(online ? .green : .secondary)
                        .accessibilityHidden(true)
                }
            }
        } icon: {
            Image(systemName: device.kind.systemImage)
        }
        .accessibilityLabel(accessibilityText)
    }

    private var accessibilityText: Text {
        let kind = String(localized: device.kind.title)
        switch (device.state, online) {
        case (.notSetUp, _):
            return Text("\(device.name), \(kind), not set up",
                        comment: "VoiceOver label of a device in the Settings sidebar that isn't set up: its name, then its kind (\"Knob\").")
        case (_, true?):
            return Text("\(device.name), \(kind), online",
                        comment: "VoiceOver label of a device in the Settings sidebar: its name, then its kind (\"Clock\").")
        case (_, false?):
            return Text("\(device.name), \(kind), offline",
                        comment: "VoiceOver label of a device in the Settings sidebar: its name, then its kind (\"Clock\").")
        default:
            return Text("\(device.name), \(kind), status unknown",
                        comment: "VoiceOver label of a device in the Settings sidebar whose reachability isn't known yet: its name, then its kind (\"Clock\").")
        }
    }
}

/// The pane for a route.
private struct SettingsDetail: View {
    let route: SettingsRoute
    let tree: SettingsTree

    var body: some View {
        switch route {
        case .app(.general):     GeneralPane()
        case .app(.connection):  ConnectionPane()
        case .app(.permissions): PermissionsPane()
        case .app(.sounds):      QuietHoursPane()
        case .source(.agents):   AgentsSourcePane()
        case .source(.focus):    FocusSourcePane()
        case .source(.weather):  WeatherSourcePane()
        case .source(.calendar): CalendarSourcePane()
        case .device(let id, let page):
            switch tree.node(for: route)?.device.kind ?? DeviceKind(deviceID: id) {
            case .clock?: ClockDetail(page: page)
            case .knob?: KnobDetail(page: page)
            case nil: ConnectionPane()
            }
        }
    }
}

private struct ClockDetail: View {
    let page: DevicePage

    var body: some View {
        switch page {
        case .hardware(.display):  ClockPage { DisplaySection() }
        case .hardware(.timeDate): ClockPage { TimeDateSection() }
        case .hardware(.buttons):  ClockPage { ButtonsSection() }
        case .hardware(.sensors):  ClockPage { SensorsSection() }
        case .hardware(.sounds):   ClockSoundsPane()
        case .hardware:            ClockStatusPane()
        case .apps:                ClockPage { RotationSection(); NativeAppsSection() }
        case .app(.agents):        ClockAgentsAppPane()
        case .app(.focus):         ClockFocusAppPane()
        case .app(.weather):       ClockWeatherAppPane()
        case .app(.calendar):      ClockCalendarAppPane()
        case .app:                 ClockPage { RotationSection(); NativeAppsSection() }
        }
    }
}
