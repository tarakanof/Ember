import Testing
import Foundation
@testable import EmberKit

private let clock = SettingsDevice(id: "clock", kind: .clock, name: "Clock", state: .ready)
private let knob = SettingsDevice(id: "knob-61fc8c", kind: .knob, name: "Desk knob", state: .ready)
private let noKnob = SettingsDevice(id: "knob", kind: .knob, name: "Knob", state: .notSetUp)
private let loadingKnob = SettingsDevice(id: "knob", kind: .knob, name: "Knob", state: .loading)
private let unreachableKnob = SettingsDevice(id: "knob", kind: .knob, name: "Knob", state: .unavailable)

// MARK: Route

@Test func storageKeyIsTheOldPaneKey() {
    #expect(SettingsRoute.storageKey == "settings.pane")
    #expect(SettingsRoute.expandedKey == "settings.expanded")
}

@Test(arguments: [
    (SettingsRoute.app(.general), "app/general"),
    (.source(.weather), "source/weather"),
    (.device("clock", .hardware(.timeDate)), "device/clock/hardware/time-date"),
    (.device("knob-61fc8c", .hardware(.display)), "device/knob-61fc8c/hardware/display"),
    (.device("clock", .hardware(.health)), "device/clock/hardware/health"),
    (.device("knob-61fc8c", .hardware(.health)), "device/knob-61fc8c/hardware/health"),
    (.device("clock", .apps), "device/clock/apps"),
    (.device("clock", .app(.weather)), "device/clock/app/weather"),
])
func routeRoundTripsThroughItsPath(route: SettingsRoute, path: String) {
    #expect(route.stored == path)
    #expect(SettingsRoute(stored: path) == route)
}

@Test(arguments: [
    ("general", SettingsRoute.app(.general)),
    ("app", .app(.general)),
    ("connection", .app(.connection)),
    ("permissions", .app(.permissions)),
    ("sounds", .app(.sounds)),
    ("clock", .device("clock", .hardware(.status))),
    ("device", .device("clock", .hardware(.status))),
    ("knob", .device("knob", .hardware(.status))),
    ("agents", .source(.agents)),
    ("display", .source(.agents)),
    ("focus", .source(.focus)),
    ("pomodoro", .source(.focus)),
    ("weather", .source(.weather)),
    ("calendar", .source(.calendar)),
    ("meetings", .source(.calendar)),
    ("reminders", .source(.calendar)),
])
func legacyPaneNameMapsToRoute(name: String, want: SettingsRoute) {
    #expect(SettingsRoute(stored: name) == want)
}

@Test(arguments: ["nonsense", "", "app/nope", "device//apps", "device/clock/hardware", "device/clock/app/x", "source/weather/x"])
func unknownStoredValueFallsBackToConnection(name: String) {
    #expect(SettingsRoute(stored: name) == .app(.connection))
}

@Test func missingStoredValueFallsBackToConnection() {
    #expect(SettingsRoute(stored: nil) == .app(.connection))
}

@Test func deviceKindFromID() {
    #expect(DeviceKind(deviceID: "clock") == .clock)
    #expect(DeviceKind(deviceID: "clock-a1b2c3") == .clock)
    #expect(DeviceKind(deviceID: "knob-61fc8c") == .knob)
    #expect(DeviceKind(deviceID: "knobby") == nil)
}

// MARK: Catalog

@Test func clockShowsEveryClockApp() {
    #expect(AppCatalog.apps(.clock) == [.agents, .focus, .weather, .calendar])
    #expect(AppCatalog.hardware(.clock) == [.status, .health, .display, .timeDate, .buttons, .sensors, .sounds])
}

@Test func knobWithoutReportShowsWhatV1FirmwareDraws() {
    #expect(AppCatalog.apps(.knob) == [.bot, .focus, .weather])
    #expect(AppCatalog.hardware(.knob) == [.status, .health, .display, .behavior])
}

@Test func knobShowsOnlyAppsItsFirmwareSupports() {
    #expect(AppCatalog.apps(.knob, supportedPages: ["bot", "weather", "nowplaying"]) == [.bot, .weather])
}

@Test func everyAppHasASource() {
    for app in AppID.allCases { #expect(AppCatalog.source(of: app) != nil) }
}

// MARK: Tree

@Test func treeGroupsAppSourcesAndDevices() {
    let tree = SettingsTree(devices: [clock, knob])
    #expect(tree.app == [.app(.general), .app(.connection), .app(.permissions), .app(.sounds)])
    #expect(tree.sources == [.source(.agents), .source(.focus), .source(.weather), .source(.calendar), .source(.music)])
    #expect(tree.devices.map(\.id) == ["clock", "knob-61fc8c"])
    let c = tree.devices[0]
    #expect(c.route == .device("clock", .hardware(.status)))
    #expect(c.hardware == [.health, .display, .timeDate, .buttons, .sensors, .sounds].map { .device("clock", .hardware($0)) })
    #expect(c.appsRoute == .device("clock", .apps))
    #expect(c.apps == [.agents, .focus, .weather, .calendar].map { .device("clock", .app($0)) })
    let k = tree.devices[1]
    #expect(k.hardware == [.health, .display, .behavior].map { .device("knob-61fc8c", .hardware($0)) })
    #expect(k.apps == [.bot, .focus, .weather].map { .device("knob-61fc8c", .app($0)) })
}

@Test func knobNotSetUpHasNoChildren() {
    let node = SettingsTree(devices: [clock, noKnob]).devices[1]
    #expect(node.hardware.isEmpty)
    #expect(node.apps.isEmpty)
    #expect(node.appsRoute == nil)
}

@Test func knobWithoutWeatherSupportHasNoWeatherNode() {
    var k = knob
    k.supportedPages = ["bot", "pomodoro"]
    let node = SettingsTree(devices: [clock, k]).devices[1]
    #expect(!node.apps.contains(.device(k.id, .app(.weather))))
}

// MARK: Resolve

@Test func resolveKeepsAValidRoute() {
    let tree = SettingsTree(devices: [clock, knob])
    for route in tree.app + tree.sources + tree.devices.flatMap({ [$0.route] + $0.hardware + $0.apps }) {
        #expect(tree.resolve(route) == route)
    }
    #expect(tree.resolve(.device("clock", .apps)) == .device("clock", .apps))
}

@Test func legacyKnobRouteFindsTheRegisteredKnob() {
    let tree = SettingsTree(devices: [clock, knob])
    #expect(tree.resolve(SettingsRoute(stored: "knob")) == .device("knob-61fc8c", .hardware(.status)))
}

@Test func hardwarePageIsRevealedUnderItsDevice() {
    let tree = SettingsTree(devices: [clock, knob])
    #expect(tree.resolve(.device("knob", .hardware(.health))) == .device("knob-61fc8c", .hardware(.health)))
    #expect(tree.expansionIDs(revealing: .device("knob-61fc8c", .hardware(.health))) == ["knob-61fc8c"])
}

@Test func goneDeviceResolvesToFirstOfItsKind() {
    let tree = SettingsTree(devices: [clock, knob])
    #expect(tree.resolve(.device("knob-000000", .app(.weather))) == .device("knob-61fc8c", .app(.weather)))
}

@Test func unknownDeviceKindResolvesToFallback() {
    let tree = SettingsTree(devices: [clock, knob])
    #expect(tree.resolve(.device("lamp-1", .apps)) == .app(.connection))
}

@Test func unsupportedAppResolvesToTheDevicesApps() {
    var k = knob
    k.supportedPages = ["bot"]
    let tree = SettingsTree(devices: [clock, k])
    #expect(tree.resolve(.device(k.id, .app(.weather))) == .device(k.id, .apps))
    #expect(tree.resolve(.device("clock", .app(.bot))) == .device("clock", .apps))
}

@Test func pageTheKindLacksResolvesToStatus() {
    let tree = SettingsTree(devices: [clock, knob])
    #expect(tree.resolve(.device(knob.id, .hardware(.sensors))) == .device(knob.id, .hardware(.status)))
    #expect(tree.resolve(.device("clock", .hardware(.behavior))) == .device("clock", .hardware(.status)))
}

@Test func routeUnderNotSetUpKnobResolvesToItsSetupNode() {
    let tree = SettingsTree(devices: [clock, noKnob])
    #expect(tree.resolve(.device("knob-61fc8c", .app(.bot))) == .device("knob", .hardware(.status)))
}

@Test func routeUnderLoadingDeviceIsHeld() {
    let tree = SettingsTree(devices: [clock, loadingKnob])
    let stored = SettingsRoute.device("knob-61fc8c", .app(.weather))
    #expect(tree.resolve(stored) == stored)
}

@Test func routeUnderUnavailableDeviceIsHeld() {
    let tree = SettingsTree(devices: [clock, unreachableKnob])
    let stored = SettingsRoute.device("knob-61fc8c", .app(.weather))
    #expect(tree.resolve(stored) == stored)
    #expect(tree.holdsStoredRoute)
}

@Test func unavailableKnobHasNoChildren() {
    let node = SettingsTree(devices: [clock, unreachableKnob]).devices[1]
    #expect(node.hardware.isEmpty)
    #expect(node.apps.isEmpty)
}

@Test func treeHoldsStoredRouteOnlyWhileADeviceIsUnknown() {
    #expect(SettingsTree(devices: [clock, loadingKnob]).holdsStoredRoute)
    #expect(!SettingsTree(devices: [clock, noKnob]).holdsStoredRoute)
    #expect(!SettingsTree(devices: [clock, knob]).holdsStoredRoute)
}

// MARK: Expansion

@Test func revealReopensACollapsedAncestor() {
    let tree = SettingsTree(devices: [clock, knob])
    let route = SettingsRoute.device("clock", .app(.agents))
    #expect(tree.expanded(["knob-61fc8c"], revealing: route) == ["knob-61fc8c", "clock", "clock/apps"])
    #expect(tree.expanded(["clock/apps"], revealing: route) == ["clock", "clock/apps"])
    #expect(tree.expanded([], revealing: .source(.weather)).isEmpty)
}

@Test func revealKeyIsStable() {
    #expect(SettingsRoute.revealKey == "settings.reveal")
}

@Test func revealingADeepRouteExpandsItsAncestors() {
    let tree = SettingsTree(devices: [clock, knob])
    #expect(tree.expansionIDs(revealing: .device("clock", .app(.weather))) == ["clock", "clock/apps"])
    #expect(tree.expansionIDs(revealing: .device("clock", .hardware(.display))) == ["clock"])
    #expect(tree.expansionIDs(revealing: .device("clock", .apps)) == ["clock"])
    #expect(tree.expansionIDs(revealing: .device("clock", .hardware(.status))).isEmpty)
    #expect(tree.expansionIDs(revealing: .source(.weather)).isEmpty)
}

@Test func expandedSetRoundTrips() {
    let set: Set = ["knob-61fc8c/apps", "clock", "clock/apps"]
    let stored = SettingsTree.storedExpanded(set)
    #expect(stored == "clock,clock/apps,knob-61fc8c/apps")
    #expect(SettingsTree.expandedSet(stored) == set)
    #expect(SettingsTree.expandedSet("").isEmpty)
}

// MARK: Shown on

@Test func sourceListsEveryDeviceAppShowingIt() {
    let tree = SettingsTree(devices: [clock, knob])
    #expect(tree.apps(showing: .weather).map(\.route)
            == [.device("clock", .app(.weather)), .device(knob.id, .app(.weather))])
    #expect(tree.apps(showing: .agents).map(\.route)
            == [.device("clock", .app(.agents)), .device(knob.id, .app(.bot))])
    #expect(tree.apps(showing: .calendar).map(\.route) == [.device("clock", .app(.calendar))])
    #expect(SettingsTree(devices: [clock, noKnob]).apps(showing: .weather).count == 1)
}

// MARK: Every old control has one new home

/// The pre-regroup panes' controls and where each lives now. A checklist
/// kept by hand: it proves each listed home is a real node and no control
/// is listed twice, not that a pane still draws the control.
private let controlHomes: [(pane: String, control: String, home: SettingsRoute)] = [
    ("agents", "preview", .device("clock", .app(.agents))),
    ("agents", "reporting", .source(.agents)),
    ("agents", "source card", .device("clock", .app(.agents))),
    ("agents", "activity card", .device("clock", .app(.agents))),
    ("agents", "usage cards", .device("clock", .app(.agents))),
    ("agents", "usage threshold", .source(.agents)),
    ("agents", "per-model usage", .device("clock", .app(.agents))),
    ("agents", "context glass", .device("clock", .app(.agents))),
    ("agents", "bottom bar", .device("clock", .app(.agents))),
    ("agents", "activity trail", .device("clock", .app(.agents))),
    ("agents", "hide when idle", .source(.agents)),
    ("agents", "attention hold", .source(.agents)),
    ("menu", "show on clock", .device("clock", .app(.agents))),
    ("focus", "preview", .device("clock", .app(.focus))),
    ("focus", "enable", .source(.focus)),
    ("focus", "durations", .source(.focus)),
    ("focus", "goals", .source(.focus)),
    ("focus", "auto-start", .source(.focus)),
    ("focus", "stop after", .source(.focus)),
    ("focus", "colors", .device("clock", .app(.focus))),
    ("weather", "preview", .device("clock", .app(.weather))),
    ("weather", "enable", .source(.weather)),
    ("weather", "location", .source(.weather)),
    ("weather", "units", .source(.weather)),
    ("weather", "refresh", .source(.weather)),
    ("weather", "air quality alert", .source(.weather)),
    ("weather", "severe alert", .source(.weather)),
    ("weather", "current tile", .device("clock", .app(.weather))),
    ("weather", "tile native icon", .device("clock", .app(.weather))),
    ("weather", "moon phase", .device("clock", .app(.weather))),
    ("weather", "overlay", .device("clock", .app(.weather))),
    ("weather", "forecast tile", .device("clock", .app(.weather))),
    ("weather", "hours ahead", .device("clock", .app(.weather))),
    ("weather", "air tile", .device("clock", .app(.weather))),
    ("weather", "popups", .device("clock", .app(.weather))),
    ("weather", "popup native icons", .device("clock", .app(.weather))),
    ("weather", "icon ids", .device("clock", .app(.weather))),
    ("calendar", "previews", .device("clock", .app(.calendar))),
    ("calendar", "show next meeting", .source(.calendar)),
    ("calendar", "tile lead", .device("clock", .app(.calendar))),
    ("calendar", "popup lead", .device("clock", .app(.calendar))),
    ("calendar", "upcoming", .source(.calendar)),
    ("calendar", "reminders access", .source(.calendar)),
    ("calendar", "reminders enable", .source(.calendar)),
    ("calendar", "reminder lead", .source(.calendar)),
    ("calendar", "next due", .source(.calendar)),
    ("calendar", "reminder hold", .device("clock", .app(.calendar))),
    ("calendar", "reminder show for", .device("clock", .app(.calendar))),
    ("calendar", "reminder native icon", .device("clock", .app(.calendar))),
    ("sounds", "quiet hours", .app(.sounds)),
    ("sounds", "clock sound", .device("clock", .hardware(.sounds))),
    ("sounds", "chimes", .device("clock", .hardware(.sounds))),
    ("sounds", "reminder sound", .device("clock", .hardware(.sounds))),
    ("clock", "status", .device("clock", .hardware(.status))),
    ("dashboard", "clock health", .device("clock", .hardware(.health))),
    ("dashboard", "knob stats", .device(knob.id, .hardware(.health))),
    ("clock", "display", .device("clock", .hardware(.display))),
    ("clock", "rotation", .device("clock", .apps)),
    ("clock", "built-in apps", .device("clock", .apps)),
    ("clock", "app colors", .device("clock", .apps)),
    ("clock", "time and date", .device("clock", .hardware(.timeDate))),
    ("clock", "sensors", .device("clock", .hardware(.sensors))),
    ("clock", "buttons", .device("clock", .hardware(.buttons))),
    ("knob", "status", .device(knob.id, .hardware(.status))),
    ("knob", "display", .device(knob.id, .hardware(.display))),
    ("knob", "pages", .device(knob.id, .apps)),
    ("knob", "poll", .device(knob.id, .hardware(.behavior))),
    ("knob", "bot", .device(knob.id, .app(.bot))),
    ("knob", "advanced", .device(knob.id, .hardware(.behavior))),
    ("general", "general", .app(.general)),
    ("connection", "connection", .app(.connection)),
    ("permissions", "permissions", .app(.permissions)),
]

@Test func everyListedControlHomeIsANodeAndListedOnce() {
    let keys = controlHomes.map { "\($0.pane)/\($0.control)" }
    #expect(Set(keys).count == keys.count, "a control is listed twice")
    let tree = SettingsTree(devices: [clock, knob])
    for c in controlHomes {
        #expect(tree.resolve(c.home) == c.home, "\(c.pane) › \(c.control) has no node: \(c.home.stored)")
    }
    let oldPanes = ["general", "connection", "clock", "knob", "agents", "focus", "weather", "calendar", "sounds", "permissions"]
    for pane in oldPanes { #expect(controlHomes.contains { $0.pane == pane }, "\(pane) lost every control") }
}
