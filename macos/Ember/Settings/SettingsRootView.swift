import SwiftUI
import EmberKit

struct SettingsRootView: View {
    @Environment(AppEnvironment.self) private var env
    @AppStorage(SettingsRoute.storageKey) private var routeName = SettingsRoute.fallback.stored
    @AppStorage(SettingsRoute.expandedKey) private var expandedName = ""
    @AppStorage(SettingsRoute.revealKey) private var reveal = 0

    static let windowWidth: CGFloat = 800

    private var tree: SettingsTree { SettingsTree(devices: devices) }

    private var devices: [SettingsDevice] {
        let knob = env.knob
        let knobState: SettingsDevice.State =
            knob.knob != nil ? .ready
            : !knob.isLoaded ? (knob.loadError == nil ? .loading : .unavailable)
            : .notSetUp
        return [
            SettingsDevice(id: clockDeviceID, kind: .clock, name: String(localized: DeviceKind.clock.title), state: .ready),
            SettingsDevice(id: knob.knob?.id ?? DeviceKind.knob.placeholderID, kind: .knob,
                           name: knob.knob?.name ?? String(localized: DeviceKind.knob.title), state: knobState,
                           supportedPages: knob.knob?.supportedPages),
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
        .task { await env.live.track(.clockHealth) }
        .task {
            while !Task.isCancelled {
                await env.knob.load()
                try? await Task.sleep(for: .seconds(30))
            }
        }
        .onAppear { settle(tree) }
        .onChange(of: routeName) { settle(tree) }
        .onChange(of: reveal) { settle(tree) }
        .onChange(of: tree) { _, new in settle(new) }
    }

    private func settle(_ tree: SettingsTree) {
        let route = tree.resolve(SettingsRoute(stored: routeName))
        let expanded = tree.expanded(SettingsTree.expandedSet(expandedName), revealing: route)
        let stored = SettingsTree.storedExpanded(expanded)
        if stored != expandedName { expandedName = stored }
        if !tree.holdsStoredRoute, route.stored != routeName { routeName = route.stored }
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
        case .notSetUp, .unavailable:
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

private struct DeviceRow: View {
    @Environment(AppEnvironment.self) private var env
    let device: SettingsDevice

    private func online(at now: Date) -> Bool? {
        switch device.kind {
        case .clock:
            return env.live.clockHealth.value?.device?.reachable
        case .knob:
            guard device.state == .ready, let knob = env.knob.knob else { return nil }
            return knob.isOnline(now: now)
        }
    }

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            let online = online(at: context.date)
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
            .accessibilityLabel(accessibilityText(online: online))
        }
    }

    private func accessibilityText(online: Bool?) -> Text {
        let kind = String(localized: device.kind.title)
        let who = device.name == kind ? device.name : "\(device.name), \(kind)"
        switch (device.state, online) {
        case (.notSetUp, _):
            return Text("\(who), not set up",
                        comment: "VoiceOver label of a Settings sidebar device that isn't set up: its name (and kind, \"Desk knob, Knob\").")
        case (.unavailable, _):
            return Text("\(who), unreachable",
                        comment: "VoiceOver label of a Settings sidebar device whose server didn't answer: its name (and kind, \"Desk knob, Knob\").")
        case (_, true?):
            return Text("\(who), online",
                        comment: "VoiceOver label of a Settings sidebar device: its name (and kind, \"Desk knob, Knob\").")
        case (_, false?):
            return Text("\(who), offline",
                        comment: "VoiceOver label of a Settings sidebar device: its name (and kind, \"Desk knob, Knob\").")
        default:
            return Text("\(who), status unknown",
                        comment: "VoiceOver label of a Settings sidebar device whose reachability isn't known yet: its name (and kind, \"Desk knob, Knob\").")
        }
    }
}

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
        case .source(.music):    MusicSourcePane()
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
        case .hardware(.health):   ClockHardwarePane()
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
