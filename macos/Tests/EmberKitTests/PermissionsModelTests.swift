import Testing
import Foundation
import Network
@testable import EmberKit

// MARK: Local Network probe

@Test(arguments: [
    (LocalNetworkProbe.BrowseOutcome.denied, LocalNetworkProbe.ServerOutcome.reachable, LocalNetworkStatus.denied),
    (.ready, .denied, .denied),
    (.ready, .notApplicable, .granted),
    (.inconclusive, .reachable, .granted),
    (.inconclusive, .unreachable, .unknown),
    (.inconclusive, .notApplicable, .unknown),
])
func probeCombines(browse: LocalNetworkProbe.BrowseOutcome, server: LocalNetworkProbe.ServerOutcome,
                   want: LocalNetworkStatus) {
    #expect(LocalNetworkProbe.combine(browse: browse, server: server) == want)
}

@Test func probeReadsBrowserStates() {
    #expect(LocalNetworkProbe.outcome(for: .failed(.dns(-65555))) == .denied)
    #expect(LocalNetworkProbe.outcome(for: .waiting(.dns(-65570))) == .denied)
    #expect(LocalNetworkProbe.outcome(for: .failed(.posix(.ENOENT))) == .inconclusive)
    #expect(LocalNetworkProbe.outcome(for: .waiting(.posix(.ENETDOWN))) == nil)
    #expect(LocalNetworkProbe.outcome(for: .ready) == .ready)
    #expect(LocalNetworkProbe.outcome(for: .setup) == nil)
}

// Any HTTP status means the request got through.
@Test func probeServerAnswers() async {
    #expect(LocalNetworkProbe.serverOutcome(for: APIError.http(status: 401, body: "")) == .reachable)
    #expect(LocalNetworkProbe.serverOutcome(for: APIError.localNetworkDenied) == .denied)
    #expect(LocalNetworkProbe.serverOutcome(for: APIError.transport("timed out")) == .unreachable)
    let down = stubbedClient { _ in
        throw URLError(.notConnectedToInternet, userInfo: ["_kCFStreamErrorDomainKey": 1, "_kCFStreamErrorCodeKey": 50])
    }
    #expect(await LocalNetworkProbe.server(down) == .denied)
    let ok = stubbedClient { req in (okResponse(req.url!), Data("ok".utf8)) }
    #expect(await LocalNetworkProbe.server(ok) == .reachable)
    let internet = APIClient(baseURL: URL(string: "https://ember.example.com"), token: nil)
    #expect(await LocalNetworkProbe.server(internet) == .notApplicable)
}

// MARK: Rows

private func snapshot(_ states: [(ProducerAgent, AgentState)], blocked: [ProducerAgent] = []) -> ProducerSnapshot {
    ProducerSnapshot(agents: states.map { (agent: $0.0, state: $0.1) }, toggle: .on, localNetworkBlocked: blocked)
}

@Test func localNetworkRowIsAlwaysRequired() {
    let off = PermissionsModel.localNetworkRow(.denied)
    #expect(off.status == .denied && off.required && off.needsAttention)
    #expect(off.action == .openSystemSettings(.localNetwork))
    #expect(!PermissionsModel.localNetworkRow(.unknown).needsAttention)
    #expect(PermissionsModel.localNetworkRow(nil).status == .checking)
}

@Test func backgroundItemsFollowTheAgents() {
    let off = PermissionsModel.backgroundItemsRow(snapshot([(.claude, .off)]))
    #expect(off.status == .notInUse && !off.required)
    let none = PermissionsModel.backgroundItemsRow(snapshot([]))
    #expect(none.status == .notInUse)

    let approval = PermissionsModel.backgroundItemsRow(snapshot([(.claude, .on), (.codex, .needsApproval)]))
    #expect(approval.status == .needsApproval && approval.needsAttention)
    #expect(approval.action == .openSystemSettings(.loginItems))

    let stuck = PermissionsModel.backgroundItemsRow(snapshot([(.claude, .notRunning)]))
    #expect(stuck.status == .notRunning && stuck.action == .repair && stuck.needsAttention)

    let on = PermissionsModel.backgroundItemsRow(snapshot([(.claude, .on), (.codex, .on)]))
    #expect(on.status == .granted && on.required && !on.needsAttention)
    #expect(on.action == .openPane(.agents))
}

@Test func helperLocalNetworkComesFromTheLinkFiles() {
    let blocked = PermissionsModel.helperLocalNetworkRow(snapshot([(.claude, .on), (.codex, .on)], blocked: [.codex]))
    #expect(blocked.status == .denied && blocked.needsAttention && blocked.blockedHelpers == [.codex])
    let fine = PermissionsModel.helperLocalNetworkRow(snapshot([(.claude, .on)]))
    #expect(fine.status == .granted)
    let idle = PermissionsModel.helperLocalNetworkRow(snapshot([(.claude, .off)]))
    #expect(idle.status == .notInUse && !idle.needsAttention)
}

@Test func remindersMatterOnlyWhenAlarmsAreOn() {
    let asked = PermissionsModel.remindersRow(.notDetermined, inUse: true)
    #expect(asked.needsAttention && asked.action == .requestAccess)
    // Alarms off: neutral, not a red Off, and the button goes to Calendar.
    let unused = PermissionsModel.remindersRow(.denied, inUse: false)
    #expect(unused.status == .notInUse && !unused.required && !unused.needsAttention)
    #expect(unused.action == .openPane(.calendar))
    #expect(PermissionsModel.remindersRow(.notDetermined, inUse: false).status == .notInUse)
    #expect(!PermissionsModel.remindersRow(.granted, inUse: true).needsAttention)
}

@Test func locationIsNeverRequired() {
    let denied = PermissionsModel.locationRow(.denied)
    #expect(denied.status == .denied && !denied.needsAttention)
    #expect(denied.action == .openSystemSettings(.location))
    #expect(PermissionsModel.locationRow(.notDetermined).action == .openPane(.weather))
}

@Test func rowsListEveryPermissionInOrder() {
    let rows = PermissionsModel.rows(localNetwork: .granted, producers: nil, reminders: .granted,
                                     remindersInUse: false, location: .notDetermined)
    #expect(rows.map(\.id) == PermissionID.allCases)
    #expect(rows.first { $0.id == .backgroundItems }?.status == .checking)
}

// MARK: Model

@MainActor
private final class FakeSources: PermissionSources {
    var network: LocalNetworkStatus = .granted
    var snapshot: ProducerSnapshot? = ProducerSnapshot(agents: [], toggle: .off)
    var remindersAccess: AccessStatus = .granted
    var remindersInUse = false
    var locationAccess: AccessStatus = .notDetermined
    /// Holds `localNetwork()` until released, to test overlapping refreshes.
    var gate: CheckedContinuation<Void, Never>?
    var holdNetwork = false
    var networkCalls = 0

    func localNetwork() async -> LocalNetworkStatus {
        networkCalls += 1
        let value = network
        if holdNetwork {
            holdNetwork = false
            await withCheckedContinuation { gate = $0 }
        }
        return value
    }
    func producers() async -> ProducerSnapshot? { snapshot }
    func reminders() -> (status: AccessStatus, inUse: Bool) { (remindersAccess, remindersInUse) }
    func location() -> AccessStatus { locationAccess }
}

@MainActor
@Test func modelStartsCheckingAndRefreshes() async {
    let fake = FakeSources()
    let model = PermissionsModel(sources: fake)
    #expect(model.rows.allSatisfy { $0.status == .checking })
    #expect(model.attention.isEmpty)

    fake.network = .denied
    fake.remindersInUse = true
    fake.remindersAccess = .denied
    await model.refresh()
    #expect(model.row(.localNetwork)?.status == .denied)
    #expect(model.attention.map(\.id) == [.localNetwork, .reminders])
    #expect(!model.isChecking && model.checkedAt != nil)

    // Fixed in System Settings, re-checked on app activation.
    fake.network = .granted
    fake.remindersAccess = .granted
    await model.refresh()
    #expect(model.attention.isEmpty)
}

// The pane's .task and didBecomeActive both refresh when Settings opens: the
// second joins the running check instead of probing again.
@MainActor
@Test func overlappingRefreshesShareOneCheck() async {
    let fake = FakeSources()
    let model = PermissionsModel(sources: fake)
    fake.network = .denied
    fake.holdNetwork = true
    let first = Task { await model.refresh(ifOlderThan: PermissionsModel.activationInterval) }
    while fake.gate == nil { await Task.yield() }
    let second = Task { await model.refresh(ifOlderThan: PermissionsModel.activationInterval) }
    let explicit = Task { await model.refresh() }
    for _ in 0..<20 { await Task.yield() }
    fake.gate?.resume()
    await first.value
    await second.value
    await explicit.value
    #expect(fake.networkCalls == 1)
    #expect(model.row(.localNetwork)?.status == .denied)
    #expect(!model.isChecking)
}

// Activations re-check at most every few seconds; Check Again always does.
@MainActor
@Test func activationRefreshesAreThrottled() async {
    let fake = FakeSources()
    var clock = Date(timeIntervalSince1970: 1_000)
    let model = PermissionsModel(sources: fake, now: { clock })
    await model.refresh(ifOlderThan: PermissionsModel.activationInterval)
    #expect(fake.networkCalls == 1)

    clock += 2
    await model.refresh(ifOlderThan: PermissionsModel.activationInterval)
    #expect(fake.networkCalls == 1)

    await model.refresh()
    #expect(fake.networkCalls == 2)

    clock += PermissionsModel.activationInterval
    await model.refresh(ifOlderThan: PermissionsModel.activationInterval)
    #expect(fake.networkCalls == 3)
}

// While re-checking, the last Local Network verdict stays up (no flicker).
@MainActor
@Test func refreshKeepsTheLastVerdictWhileProbing() async {
    let fake = FakeSources()
    let model = PermissionsModel(sources: fake)
    fake.network = .denied
    await model.refresh()
    fake.holdNetwork = true
    let again = Task { await model.refresh() }
    while fake.gate == nil { await Task.yield() }
    #expect(model.row(.localNetwork)?.status == .denied)
    #expect(model.isChecking)
    fake.gate?.resume()
    await again.value
}
