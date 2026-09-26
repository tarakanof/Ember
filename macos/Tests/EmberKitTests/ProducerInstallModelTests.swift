import Testing
import Foundation
@testable import EmberKit

@MainActor @Test func producerModelCachesSnapshotAndReportsFailures() async {
    let sm = FakeSMAppService()
    let svc = ProducerInstallService(sm: sm, runner: FailingForCodexRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { _ in true })
    let m = ProducerInstallModel(service: svc)
    #expect(m.snapshot == nil)
    await m.refresh()
    #expect(m.snapshot?.agents.count == 2)
    #expect(!m.isOn)
    await m.setEnabled(true)
    #expect(!m.isWorking)
    #expect(m.failure?.hasPrefix("Codex") == true)
    #expect(m.lastRunSucceeded == false)
    #expect(m.snapshot?.toggle == .partial)
}

@MainActor @Test func producerModelTurnsOn() async {
    let svc = ProducerInstallService(sm: FakeSMAppService(), runner: FakeRunner(),
        bundleMacOSDir: URL(fileURLWithPath: "/A/Contents/MacOS"), home: URL(fileURLWithPath: "/Users/x"),
        fileExists: { $0.hasSuffix(".claude") })
    let m = ProducerInstallModel(service: svc)
    await m.setEnabled(true)
    #expect(m.isOn)
    #expect(m.failure == nil)
    #expect(m.lastRunSucceeded == true)
}
