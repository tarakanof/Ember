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
        let s = try state(#"{"enabled":true,"phase":"offline","last_error":"\#(code)"}"#)
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
    #expect(PrinterStatusLine(nil, error: .featureOff) == .unavailable(featureOff: true))
    #expect(PrinterStatusLine(nil, error: .offline) == .unavailable(featureOff: false))
    #expect(PrinterStatusLine(nil, error: .offline).isProblem)
}

@Test func addressesAreValidatedLikeTheServer() {
    #expect(PrinterConfig.validatedAddress(" http://192.168.1.50/ ") == "http://192.168.1.50")
    #expect(PrinterConfig.validatedAddress("https://core.local:8443") == "https://core.local:8443")
    for bad in ["", "192.168.1.50", "http://", "ftp://core.local", "http://u:p@core.local",
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
        config: ServerConfigModel(initial: PrinterConfig(), load: { PrinterConfig() }, save: { _ in }),
        state: { PrinterState(enabled: true, phase: "idle") },
        preview: { throw APIError.http(status: 404, body: "not found") },
        previewModel: preview)
    model.refreshPreview()
    for _ in 0..<50 { await Task.yield() }
    #expect(preview.response == nil && preview.isUnavailable)
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
    model.addressText = "192.168.1.50"
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
        config: ServerConfigModel(initial: PrinterConfig(), load: { PrinterConfig() }, save: { _ in }),
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
    #expect(model.status == .unavailable(featureOff: true))
}
