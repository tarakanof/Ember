import Testing
import Foundation
@testable import EmberKit

private func state(_ json: String) throws -> PrinterState {
    try JSONDecoder().decode(PrinterState.self, from: Data(json.utf8))
}

@Test func connectedShowsTheFirmware() throws {
    let s = try state(#"{"enabled":true,"phase":"idle","printer":{"name":"CORE One","host":"core.local","discovered":true,"firmware":"6.3.0"}}"#)
    #expect(PrinterStatusLine(s) == .connected(firmware: "6.3.0"))
}

@Test func everyLastErrorHasItsLine() throws {
    let cases: [(String, PrinterStatusLine)] = [
        ("wrong_password", .wrongPassword),
        ("no_password", .passwordMissing),
        ("not_found", .notFound),
        ("unreachable", .printerOff),
        ("something_new", .other("something_new")),
    ]
    for (code, line) in cases {
        let phase = code == "no_password" ? "off" : "offline"
        let s = try state(#"{"enabled":true,"phase":"\#(phase)","last_error":"\#(code)"}"#)
        #expect(PrinterStatusLine(s) == line, "\(code)")
    }
}

@Test func theLinesReadAsTheIssueWords() {
    func text(_ l: PrinterStatusLine) -> String { String(localized: l.title) }
    #expect(text(.connected(firmware: "6.3.0")) == "Connected · firmware 6.3.0")
    #expect(text(.connected(firmware: "")) == "Connected")
    #expect(text(.wrongPassword) == "Wrong password")
    #expect(text(.passwordMissing) == "Password missing on the server")
    #expect(text(.notFound) == "Not found on the network")
    #expect(text(.printerOff) == "Printer off")
}

@Test func theMissingPasswordLineNamesTheEnvVarAndWhereToRead() throws {
    let detail = try #require(PrinterStatusLine.passwordMissing.detail)
    let s = String(localized: detail)
    #expect(s.contains("EMBER_PRUSALINK_PASSWORD") && s.contains("Settings → Network → PrusaLink"))
}

@Test func disabledAndUnknownAreNotProblems() throws {
    #expect(PrinterStatusLine(nil) == .unknown)
    let off = try state(#"{"enabled":false,"phase":"off","last_error":"no_password"}"#)
    #expect(PrinterStatusLine(off) == .disabled)
    #expect(!PrinterStatusLine.disabled.isProblem && !PrinterStatusLine.unknown.isProblem)
    #expect(PrinterStatusLine.wrongPassword.isProblem)
}

@Test func enabledWithPhaseOffAndNoErrorIsStillChecking() throws {
    let s = try state(#"{"enabled":true,"phase":"off"}"#)
    #expect(PrinterStatusLine(s) == .unknown)
}

@Test func aFailedFetchIsShownNotHiddenAsChecking() {
    #expect(PrinterStatusLine(nil, error: .featureOff) == .unavailable(.featureOff))
    #expect(PrinterStatusLine(nil, error: .offline) == .unavailable(.offline))
    #expect(PrinterStatusLine(nil, error: .offline).isProblem)
}

@Test func addressesAreValidatedLikeTheServer() {
    #expect(PrinterConfig.validatedAddress(" http://192.168.1.50/ ") == "http://192.168.1.50")
    #expect(PrinterConfig.validatedAddress("https://core.local:8443") == "https://core.local:8443")
    #expect(PrinterConfig.validatedAddress("192.168.1.50") == "http://192.168.1.50")
    #expect(PrinterConfig.validatedAddress("core.local:8080") == "http://core.local:8080")
    for bad in ["", "http://", "ftp://core.local", "http://u:p@core.local",
                "http://core.local/path", "http://core.local?x=1"] {
        #expect(PrinterConfig.validatedAddress(bad) == nil, "\(bad)")
    }
}

@Test func configRoundTripsTheServerKeys() throws {
    let json = #"{"enabled":true,"url":"http://10.0.0.5","name":"Mk4","clock_app":false,"notify_done":true,"notify_attention":false,"done_hold_min":45}"#
    let c = try JSONDecoder().decode(PrinterConfig.self, from: Data(json.utf8))
    #expect(c == PrinterConfig(enabled: true, url: "http://10.0.0.5", name: "Mk4", clockApp: false,
                               notifyDone: true, notifyAttention: false, doneHoldMin: 45))
    let out = try JSONSerialization.jsonObject(with: JSONEncoder().encode(c)) as? [String: Any]
    #expect(out?["clock_app"] as? Bool == false && out?["done_hold_min"] as? Int == 45)
    #expect(out?["password"] == nil)
}

@Test func emptyUrlMeansAuto() throws {
    let c = try JSONDecoder().decode(PrinterConfig.self, from: Data(#"{"url":""}"#.utf8))
    #expect(c.isAuto && c.manualURL.isEmpty)
}

@MainActor
@Test func previewStaysAbsentWhileTheRouteIsMissing() async {
    let preview = PreviewModel(debounce: .zero, sleep: { _ in })
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { throw APIError.http(status: 404, body: "not found") },
        previewModel: preview)
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    #expect(preview.response == nil && preview.isUnavailable)
    #expect(model.previewResponse == nil && model.previewMissing)
    await model.refreshState()
    #expect(model.status == .connected(firmware: ""))
}

@MainActor
private func loadedModel(_ c: PrinterConfig, saves: SaveLog = SaveLog()) async -> PrinterSettingsModel {
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(), load: { c }, save: { await saves.add($0) }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.load()
    return model
}

private actor SaveLog {
    private(set) var saved: [PrinterConfig] = []
    func add(_ c: PrinterConfig) { saved.append(c) }
}

@MainActor
@Test func switchingToManualChangesNothingUntilAValidAddressIsCommitted() async {
    let model = await loadedModel(PrinterConfig(enabled: true))
    #expect(!model.manualMode)
    model.chooseManual()
    #expect(model.manualMode && model.config.draft.isAuto && !model.config.hasUnsavedChanges)
    model.addressText = "http://"
    model.commitAddress()
    #expect(model.addressInvalid && model.config.draft.isAuto)
    model.addressText = "http://192.168.1.50"
    model.commitAddress()
    #expect(!model.addressInvalid && model.config.draft.url == "http://192.168.1.50")
}

@MainActor
@Test func aManualServerAddressOpensInManualMode() async {
    let model = await loadedModel(PrinterConfig(enabled: true, url: "http://10.0.0.5"))
    #expect(model.manualMode && model.addressText == "http://10.0.0.5")
    model.chooseAuto()
    #expect(!model.manualMode && model.config.draft.isAuto)
}

@MainActor
@Test func aSaveRefreshesTheState() async {
    actor Calls { var n = 0; func bump() { n += 1 } }
    let calls = Calls()
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { await calls.bump(); return PrinterState(enabled: true, phase: "idle") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.saved()
    #expect(await calls.n == 1 && model.status == .connected(firmware: ""))
}

@MainActor
@Test func aFailedFetchLeavesAnUnavailableLine() async {
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(), load: { PrinterConfig() }, save: { _ in }),
        state: { throw APIError.http(status: 404, body: "") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.refreshState()
    #expect(model.status == .unavailable(.featureOff))
}

@MainActor
@Test func aReloadKeepsTheManualChoiceAndTheTypedAddress() async {
    let model = await loadedModel(PrinterConfig(enabled: true))
    model.chooseManual()
    model.addressText = "http://192.168"
    await model.load()
    #expect(model.manualMode && model.addressText == "http://192.168")
}

@MainActor
@Test func aReloadDuringAPendingSaveDoesNotFlipThePicker() async {
    let model = await loadedModel(PrinterConfig(enabled: true))
    model.chooseManual()
    model.addressText = "http://192.168.1.50"
    model.commitAddress()
    await model.load()
    #expect(model.manualMode && model.config.draft.url == "http://192.168.1.50")
}

@MainActor
@Test func manualAutoManualCommitsTheAddressTheFieldShows() async {
    let model = await loadedModel(PrinterConfig(enabled: true, url: "http://10.0.0.5"))
    model.chooseAuto()
    #expect(model.config.draft.isAuto)
    model.chooseManual()
    #expect(model.manualMode && model.config.draft.url == "http://10.0.0.5" && !model.addressInvalid)
}

@MainActor
@Test func anEmptyManualFieldNeitherWarnsNorChangesAnything() async {
    let model = await loadedModel(PrinterConfig(enabled: true))
    model.chooseManual()
    model.commitAddress()
    #expect(!model.addressInvalid && model.config.draft.isAuto)
}

@MainActor
@Test func foundHostNeedsADiscoveredPrinterWithAHost() async {
    let discovered = PrinterState(enabled: true, phase: "idle",
                                  printer: PrinterInfo(host: "core.local", discovered: true))
    let manual = PrinterState(enabled: true, phase: "idle",
                              printer: PrinterInfo(host: "core.local", discovered: false))
    func model(_ s: PrinterState) async -> PrinterSettingsModel {
        let m = PrinterSettingsModel(
            config: ServerConfigModel(initial: PrinterConfig(), load: { PrinterConfig() }, save: { _ in }),
            state: { s }, preview: { throw APIError.http(status: 404, body: "") })
        await m.refreshState()
        return m
    }
    #expect(await model(discovered).foundHost == "core.local")
    #expect(await model(manual).foundHost == nil)
}

@MainActor
@Test func turningItOnShowsCheckingUntilTheServerCatchesUp() async {
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { PrinterState(enabled: false, phase: "off") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.load()
    #expect(model.status == .unknown)
}

@MainActor
@Test func noPreviewIsRequestedWhileThePrinterIsOff() async {
    actor Calls { var n = 0; func bump() { n += 1 } }
    let calls = Calls()
    let preview = PreviewModel(debounce: .zero, sleep: { _ in })
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(), load: { PrinterConfig() }, save: { _ in }),
        state: { PrinterState() },
        preview: { await calls.bump(); throw APIError.http(status: 404, body: "") },
        previewModel: preview)
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    #expect(await calls.n == 0)
}

@MainActor
@Test func aTransientPreviewErrorKeepsTheLastFrameAndPolling() async {
    final class Box: @unchecked Sendable { var fail = false }
    let box = Box()
    let frame = PreviewResponse(width: 32, height: 8, activity: "print", frames: [CardFrame(card: "print", pixels: [])])
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { if box.fail { throw APIError.timedOut }; return frame },
        previewModel: PreviewModel(debounce: .zero, sleep: { _ in }))
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    #expect(model.previewResponse == frame)
    box.fail = true
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    #expect(model.previewResponse == frame && !model.previewMissing)
}

@MainActor
@Test func turningThePrinterOffHidesThePreview() async {
    let frame = PreviewResponse(width: 32, height: 8, activity: "print", frames: [CardFrame(card: "print", pixels: [])])
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { frame },
        previewModel: PreviewModel(debounce: .zero, sleep: { _ in }))
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    #expect(model.previewResponse == frame)
    model.config.draft.enabled = false
    #expect(model.previewResponse == nil)
}

@MainActor
@Test func aBlipKeepsTheLastStateUntilItRepeats() async {
    final class Box: @unchecked Sendable { var fail = false }
    let box = Box()
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { if box.fail { throw APIError.timedOut }; return PrinterState(enabled: true, phase: "idle", printer: PrinterInfo(firmware: "6.3.0")) },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.refreshState()
    box.fail = true
    await model.refreshState()
    await model.refreshState()
    #expect(model.status == .connected(firmware: "6.3.0"))
    await model.refreshState()
    #expect(model.status == .unavailable(.timedOut))
}

@MainActor
@Test func clearingAManualFieldRestoresTheSavedAddress() async {
    let model = await loadedModel(PrinterConfig(enabled: true, url: "http://10.0.0.5"))
    model.addressText = ""
    model.commitAddress()
    #expect(model.addressText == "http://10.0.0.5" && model.config.draft.url == "http://10.0.0.5")
}

@MainActor
@Test func aReloadPicksUpAnAddressAnotherMacChanged() async {
    final class Box: @unchecked Sendable { var url = "auto" }
    let box = Box()
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(), load: { PrinterConfig(enabled: true, url: box.url) }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.load()
    #expect(!model.manualMode)
    box.url = "http://10.0.0.9"
    await model.load()
    #expect(model.manualMode && model.addressText == "http://10.0.0.9")
}

@MainActor
@Test func thePreviewFollowsTheClockToggle() async {
    let frame = PreviewResponse(width: 32, height: 8, activity: "print", frames: [CardFrame(card: "print", pixels: [])])
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { frame },
        previewModel: PreviewModel(debounce: .zero, sleep: { _ in }))
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    model.config.draft.clockApp = false
    #expect(model.previewResponse == nil)
}

@MainActor
@Test func aFailedSaveOfTheToggleDoesNotLeaveCheckingForever() async {
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(), load: { PrinterConfig() }, save: { _ in throw APIError.timedOut }),
        state: { PrinterState(enabled: false, phase: "off") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.load()
    model.config.draft.enabled = true
    await model.config.saveNow()
    #expect(model.config.saveError != nil && model.status == .disabled)
}

@MainActor
@Test func theSameServerKeepsOneModelAcrossVisits() {
    let cache = PrinterModelCache()
    let a = APIClient(baseURL: URL(string: "http://10.0.0.2:3627"), token: "t")
    let again = APIClient(baseURL: URL(string: "http://10.0.0.2:3627"), token: "t")
    let other = APIClient(baseURL: URL(string: "http://10.0.0.3:3627"), token: "t")
    let retoken = APIClient(baseURL: URL(string: "http://10.0.0.2:3627"), token: "u")
    #expect(cache.model(for: a) === cache.model(for: again))
    #expect(cache.model(for: other) !== cache.model(for: a))
    #expect(cache.model(for: retoken) !== cache.model(for: other))
}

@MainActor
@Test func noPreviewIsRequestedWhileTheClockAppIsOff() async {
    actor Calls { var n = 0; func bump() { n += 1 } }
    let calls = Calls()
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true, clockApp: false),
                                  load: { PrinterConfig(enabled: true, clockApp: false) }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { await calls.bump(); throw APIError.http(status: 404, body: "") },
        previewModel: PreviewModel(debounce: .zero, sleep: { _ in }))
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    #expect(await calls.n == 0)
}

@MainActor
@Test func reopeningAfterAHalfChosenManualAddressMatchesTheServer() async {
    let model = await loadedModel(PrinterConfig(enabled: true))
    model.chooseManual()
    model.addressText = "http://"
    model.commitAddress()
    #expect(model.manualMode && model.addressInvalid)
    model.settleAddress()
    #expect(!model.manualMode && !model.addressInvalid && model.addressText.isEmpty)
}

@MainActor
@Test func settlingKeepsAnUnsavedAddress() async {
    let model = await loadedModel(PrinterConfig(enabled: true))
    model.chooseManual()
    model.addressText = "http://192.168.1.50"
    model.commitAddress()
    model.settleAddress()
    #expect(model.manualMode && model.addressText == "http://192.168.1.50")
}

@MainActor
@Test func turningItOffShowsOffAtOnce() async {
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: false), load: { PrinterConfig() }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "offline", lastError: "wrong_password") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.load()
    #expect(model.status == .disabled)
}

@MainActor
@Test func aSuccessBetweenBlipsResetsTheFailureCount() async {
    final class Box: @unchecked Sendable { var fail = false }
    let box = Box()
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { if box.fail { throw APIError.timedOut }; return PrinterState(enabled: true, phase: "idle") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.refreshState()
    for _ in 0..<5 {
        box.fail = true
        await model.refreshState()
        await model.refreshState()
        box.fail = false
        await model.refreshState()
    }
    #expect(model.status == .connected(firmware: ""))
}

@MainActor
@Test func aRefusedTokenClearsTheStateAtOnce() async {
    final class Box: @unchecked Sendable { var fail = false }
    let box = Box()
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { if box.fail { throw APIError.http(status: 401, body: "") }; return PrinterState(enabled: true, phase: "idle") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.refreshState()
    box.fail = true
    await model.refreshState()
    #expect(model.status == .unavailable(.unauthorized))
}

@MainActor
@Test func theStatusWaitsForTheServerToTakeANewAddress() async {
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true, url: "http://10.0.0.99"),
                                  load: { PrinterConfig(enabled: true, url: "http://10.0.0.99") }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle",
                              printer: PrinterInfo(host: "core.local", url: "http://10.0.0.5", discovered: true, firmware: "6.3.0")) },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.load()
    #expect(model.status == .unknown)
}

@MainActor
@Test func aManualPrinterThatIsUnreachableShowsItsOwnLine() async {
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true, url: "http://10.0.0.99"),
                                  load: { PrinterConfig(enabled: true, url: "http://10.0.0.99") }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "offline", lastError: "unreachable",
                              printer: PrinterInfo(url: "http://10.0.0.99")) },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.load()
    #expect(model.status == .printerOff)
}

@MainActor
@Test func theStatusIsNotOffBeforeTheConfigLoads() async {
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(), load: { throw APIError.timedOut }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { throw APIError.http(status: 404, body: "") })
    await model.load()
    #expect(model.status == .connected(firmware: ""))
}

@MainActor
@Test func closingThePaneForgetsTheLiveStateAndFrame() async {
    let frame = PreviewResponse(width: 32, height: 8, activity: "print", frames: [CardFrame(card: "print", pixels: [])])
    let model = PrinterSettingsModel(
        config: ServerConfigModel(initial: PrinterConfig(enabled: true), load: { PrinterConfig(enabled: true) }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { frame },
        previewModel: PreviewModel(debounce: .zero, sleep: { _ in }))
    await model.load()
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    #expect(model.previewResponse == frame && model.status == .connected(firmware: ""))
    model.resetLive()
    #expect(model.previewResponse == nil && model.status == .unknown)
}

@Test func aRefusedTokenPointsAtTheToken() {
    let detail = PrinterStatusLine.unavailable(.unauthorized).detail
    #expect(detail.map { String(localized: $0).contains("token") } == true)
}
