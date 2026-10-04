import Testing
import Foundation
@testable import EmberKit

private let home = URL(fileURLWithPath: "/Users/x")

private func service(_ sm: FakeSMAppService = FakeSMAppService(), runner: ProducerCommandRunning = FakeRunner(),
                     files: [String: String] = [:], dirs: Set<String> = [],
                     prefs: InMemoryProducerPrefs = InMemoryProducerPrefs()) -> ProducerInstallService {
    ProducerInstallService(sm: sm, runner: runner,
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: home,
        fileExists: { dirs.contains($0) || files[$0] != nil },
        readFile: { files[$0].map { Data($0.utf8) } },
        prefs: prefs, uid: 501)
}

private func hookFixtures() -> URL {
    URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent()
        .appendingPathComponent("cmd/ember-claude-producer/testdata/hook-registration")
}

@Test func claudeHookFixturesReadLikeTheGoProducer() throws {
    struct Expected: Decodable {
        let plugin: Bool
        let settings_events: Int
        let unreadable: Bool
    }
    let dir = hookFixtures()
    let expected = try JSONDecoder().decode([String: Expected].self,
                                            from: Data(contentsOf: dir.appendingPathComponent("expected.json")))
    #expect(!expected.isEmpty)
    for (name, want) in expected {
        let got = ClaudeHookRegistration.read(settingsJSON: try Data(contentsOf: dir.appendingPathComponent(name)),
                                              killSwitch: false)
        #expect(got.pluginEnabled == want.plugin, "\(name)")
        #expect(got.settingsEvents == want.settings_events, "\(name)")
        #expect(got.settingsUnreadable == want.unreadable, "\(name)")
    }
}

@Test func claudeHookSourceFollowsPluginAndSettings() {
    func source(_ plugin: Bool, _ events: Int) -> ClaudeHookRegistration.Source {
        ClaudeHookRegistration(pluginEnabled: plugin, settingsEvents: events, killSwitch: false).source
    }
    #expect(source(true, 0) == .plugin)
    #expect(source(false, 3) == .settings)
    #expect(source(true, 3) == .both)
    #expect(source(false, 0) == .none)
    #expect(ClaudeHookRegistration.read(settingsJSON: nil, killSwitch: false).source == .none)
}

@Test func claudeHooksNoticeOnlyWarnsWhileReporting() {
    func notice(_ plugin: Bool, _ events: Int, kill: Bool, on: Bool) -> ClaudeHooksNotice {
        .notice(for: ClaudeHookRegistration(pluginEnabled: plugin, settingsEvents: events, killSwitch: kill),
                reportingOn: on)
    }
    #expect(notice(true, 0, kill: false, on: true) == .fine)
    #expect(notice(false, 8, kill: false, on: true) == .fine)
    #expect(notice(true, 8, kill: false, on: true) == .registeredTwice)
    #expect(notice(false, 0, kill: false, on: true) == .missing)
    #expect(notice(true, 0, kill: true, on: true) == .paused)
    #expect(notice(true, 8, kill: true, on: false) == .fine)
    #expect(notice(false, 0, kill: false, on: false) == .fine)
    #expect(!ClaudeHooksNotice.fine.offersConfigure)
    let unreadable = ClaudeHookRegistration.read(settingsJSON: Data("{,".utf8), killSwitch: false)
    #expect(unreadable.settingsUnreadable)
    #expect(ClaudeHookRegistration.read(settingsJSON: nil, killSwitch: false).settingsUnreadable == false)
    #expect(ClaudeHooksNotice.notice(for: unreadable, reportingOn: true) == .settingsUnreadable)
    #expect(!ClaudeHooksNotice.settingsUnreadable.offersConfigure)
    #expect(ClaudeHooksNotice.registeredTwice.offersConfigure)
}

@Test func claudeHookRegistrationReadsTheKillSwitchAndSettingsUnderHome() {
    let svc = service(files: [
        "/Users/x/.claude/settings.json": #"{"enabledPlugins":{"ember@ember":true}}"#,
        "/Users/x/.config/ember/claude-hooks.disabled": "x",
    ])
    #expect(svc.claudeHookRegistration() == ClaudeHookRegistration(pluginEnabled: true, settingsEvents: 0, killSwitch: true,
                                                                   settingsUnreadable: false))
}

@Test func t3IsDetectedWhereTheHelperLooks() {
    #expect(service(dirs: ["/Users/x/.t3"]).detectedAgents() == [.t3])
    #expect(service(files: ["/Users/x/.config/ember/producer.env": "EMBER_T3_HOME=~/Work/t3\n"],
                    dirs: ["/Users/x/Work/t3"]).detectedAgents() == [.t3])
    #expect(service(files: ["/Users/x/.config/ember/producer.env": "EMBER_T3_HOME=/opt/t3\n"],
                    dirs: ["/opt/t3"]).detectedAgents() == [.t3])
    #expect(service(dirs: ["/opt/t3"]).detectedAgents().isEmpty)
}

@MainActor @Test func undetectedT3IsListedButLeftOutOfTheToggle() async {
    let sm = FakeSMAppService(); sm.statuses = ["com.ember.heartbeat.plist": .enabled]
    let snap = await service(sm, dirs: ["/Users/x/.claude"]).snapshot()
    #expect(snap.agents.map(\.agent) == [.claude, .t3])
    #expect(snap.undetected == [.t3])
    #expect(!snap.noToolDetected)
    #expect(snap.toggle == .on)
    #expect(await service().snapshot().noToolDetected)
}

@MainActor @Test func t3CanBeTurnedOnUndetectedAndTheMasterSwitchTurnsItOff() async {
    let sm = FakeSMAppService(); let runner = FakeRunner()
    let svc = service(sm, runner: runner)
    let on = await svc.setEnabled(.t3, true)
    #expect(on.allSatisfy { $0.error == nil })
    #expect(runner.calls.first?.0 == "/A/Contents/MacOS/ember-t3-producer")
    #expect(runner.calls.first?.1 == ["configure"])
    #expect(sm.registered == ["com.ember.t3.plist"])
    #expect(svc.managedAgents() == [.t3])
    #expect(svc.toggleState() == .on)
    let off = await svc.uninstallAll()
    #expect(off.map(\.agent) == [.t3])
    #expect(sm.unregistered == ["com.ember.t3.plist"])
    #expect(svc.managedAgents().isEmpty)
}

@MainActor @Test func turningClaudeOffRunsDeconfigureForThePluginsKillSwitch() async {
    let sm = FakeSMAppService(); sm.statuses = ["com.ember.heartbeat.plist": .enabled]
    let runner = FakeRunner()
    _ = await service(sm, runner: runner, dirs: ["/Users/x/.claude"]).setEnabled(.claude, false)
    #expect(sm.unregistered == ["com.ember.heartbeat.plist"])
    #expect(runner.calls.map(\.1) == [["deconfigure"]])
    #expect(runner.calls.first?.0 == "/A/Contents/MacOS/ember-claude-producer")
}

@MainActor @Test func snapshotCarriesTheClaudeHooksNotice() async {
    let sm = FakeSMAppService(); sm.statuses = ["com.ember.heartbeat.plist": .enabled]
    let settings = #"{"enabledPlugins":{"ember@ember":true},"hooks":{"Stop":[{"hooks":[{"command":"ember-claude-producer hook Stop"}]}]}}"#
    let snap = await service(sm, files: ["/Users/x/.claude/settings.json": settings], dirs: ["/Users/x/.claude"]).snapshot()
    #expect(snap.claudeHooks?.source == .both)
    #expect(snap.claudeHooksNotice == .registeredTwice)
    let noClaude = await service().snapshot()
    #expect(noClaude.claudeHooks == nil)
    #expect(noClaude.claudeHooksNotice == .fine)
}

@MainActor @Test func fixingClaudeHooksRunsConfigureAndReportsAFailure() async {
    let runner = FakeRunner()
    let svc = service(runner: runner, dirs: ["/Users/x/.claude"])
    let ok = await svc.configureClaudeHooks()
    #expect(ok.map(\.agent) == [.claude])
    #expect(ok.allSatisfy { $0.error == nil })
    #expect(runner.calls.map(\.1) == [["configure"]])
    runner.exitFor = { _ in 1 }
    let model = ProducerInstallModel(service: svc)
    await model.configureClaudeHooks()
    #expect(model.lastRunSucceeded == false)
    #expect(model.failure?.hasPrefix("Claude") == true)
}
