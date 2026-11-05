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

@Test func enabledWithPhaseOffAndNoErrorMeansNoPassword() throws {
    let s = try state(#"{"enabled":true,"phase":"off"}"#)
    #expect(PrinterStatusLine(s) == .passwordMissing)
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
