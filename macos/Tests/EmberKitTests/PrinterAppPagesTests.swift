import Testing
import Foundation
@testable import EmberKit

private final class PrintServer: @unchecked Sendable {
    private let lock = NSLock()
    private(set) var puts: [(String, String)] = []
    var printPageOn = false
    var pages = #"["bot","print"]"#
    var printerBody = #"{"enabled":true,"url":"http://10.0.0.5","name":"Mk4","clock_app":true,"notify_done":true,"notify_attention":true,"done_hold_min":30,"show":"printing"}"#

    func handle(_ req: URLRequest) -> (HTTPURLResponse, Data) {
        let body = String(decoding: req.httpBody ?? req.httpBodyStreamData() ?? Data(), as: UTF8.self)
        let method = req.httpMethod ?? "GET"
        let path = req.url!.path
        lock.withLock { if method == "PUT" { puts.append((path, body)) } }
        switch (method, path) {
        case ("GET", "/v1/print/config"), ("PUT", "/v1/print/config"):
            return (okResponse(req.url!), Data(printerBody.utf8))
        case ("GET", "/v1/devices"):
            return (okResponse(req.url!), Data(device.utf8))
        case ("GET", "/v1/devices/knob-61fc8c/config"):
            return (okResponse(req.url!), Data(config.utf8))
        case ("PUT", "/v1/devices/knob-61fc8c/config"):
            if body.contains(#""on":true"#) { lock.withLock { printPageOn = true } }
            return (okResponse(req.url!), Data(config.utf8))
        default:
            return (okResponse(req.url!, status: 404), Data(#"{"error":"not found"}"#.utf8))
        }
    }

    var config: String {
        let on = lock.withLock { printPageOn }
        return #"{"brightness":{"follow_ember":true,"level":153,"floor":10,"startup":153},"pages":[{"id":"bot","on":true},{"id":"print","on":\#(on)}],"home":"bot","poll_ms":2000,"bot":{"sleepy_after_s":300,"demo_hold_s":20}}"#
    }

    var device: String { #"{"devices":[{"id":"knob-61fc8c","kind":"cinder-knob","hw_id":"3cdc7561fc8c","name":"Desk knob","created_at":"2026-10-04T10:00:00Z","config_version":3,"rotation_pending":false,"rotated_at":null,"effective_caps":{"view":[1,1],"pages":\#(pages),"features":["print_moments"],"source":"reported"}}]}"# }
}

@MainActor
private func knobModel(_ server: PrintServer, marks: MemoryKnobPageMarks = MemoryKnobPageMarks()) async -> KnobModel {
    let m = KnobModel(service: KnobService(client: stubbedClient(token: "t") { server.handle($0) }),
                      ports: KnobSerialPorts(scanner: { [] }), opener: FakeOpener(FakeKnob()),
                      debounce: .zero, sleep: { _ in })
    m.pageMarks = marks
    await m.load()
    return m
}

@MainActor
@Test func theClockPageEditsTheSameServerSettingsAsBefore() async throws {
    let server = PrintServer()
    let model = PrinterSettingsModel(client: stubbedClient(token: "t") { server.handle($0) })
    await model.config.load()
    model.config.draft.clockApp = false
    model.config.draft.show = .on
    model.config.draft.notifyDone = false
    model.config.draft.notifyAttention = false
    model.config.draft.doneHoldMin = 45
    await model.config.saveNow()
    let put = try #require(server.puts.last)
    #expect(put.0 == "/v1/print/config")
    let sent = try #require(try JSONSerialization.jsonObject(with: Data(put.1.utf8)) as? [String: Any])
    #expect(sent["clock_app"] as? Bool == false && sent["show"] as? String == "on")
    #expect(sent["notify_done"] as? Bool == false && sent["notify_attention"] as? Bool == false)
    #expect(sent["done_hold_min"] as? Int == 45)
    #expect(sent["enabled"] as? Bool == true && sent["url"] as? String == "http://10.0.0.5" && sent["name"] as? String == "Mk4")
}

@MainActor
@Test func switchingThePrinterOnTellsTheCacheOwner() async {
    let server = PrintServer()
    server.printerBody = #"{"enabled":false}"#
    let cache = PrinterModelCache()
    var fired = 0
    cache.onSwitchedOn = { fired += 1 }
    let model = cache.model(for: stubbedClient(token: "t") { server.handle($0) })
    await model.config.load()
    model.config.draft.enabled = true
    server.printerBody = #"{"enabled":true}"#
    await model.config.saveNow()
    #expect(fired == 1)
    model.config.draft.url = "http://10.0.0.9"
    await model.config.saveNow()
    #expect(fired == 1, "only the off to on edge counts")
}

@MainActor
@Test func anUntouchedPrintPageIsTurnedOn() async throws {
    let server = PrintServer()
    let marks = MemoryKnobPageMarks()
    let knob = await knobModel(server, marks: marks)
    #expect(knob.settings.draft.pages.first { $0.id == "print" }?.on == false)
    #expect(await knob.turnOnPrintPageIfUntouched())
    #expect(knob.settings.applied?.pages.first { $0.id == "print" }?.on == true)
    #expect(marks.isDecided(knob: "knob-61fc8c", page: "print"))
    let put = try #require(server.puts.last)
    #expect(put.0 == "/v1/devices/knob-61fc8c/config" && put.1.contains(#""print""#))
}

@MainActor
@Test func aPageTheUserSwitchedOffIsLeftAlone() async {
    let server = PrintServer()
    let marks = MemoryKnobPageMarks()
    let knob = await knobModel(server, marks: marks)
    knob.edit { $0.pages[1].on = true }
    knob.edit { $0.pages[1].on = false }
    #expect(marks.isDecided(knob: "knob-61fc8c", page: "print"))
    let before = server.puts.count
    #expect(!(await knob.turnOnPrintPageIfUntouched()))
    #expect(knob.settings.draft.pages.first { $0.id == "print" }?.on == false)
    #expect(server.puts.count == before)
}

@MainActor
@Test func theDefaultIsAppliedOnceEvenIfTheUserTurnsItOffLater() async {
    let server = PrintServer()
    let knob = await knobModel(server)
    #expect(await knob.turnOnPrintPageIfUntouched())
    knob.edit { $0.pages[1].on = false }
    await knob.settings.saveNow()
    #expect(!(await knob.turnOnPrintPageIfUntouched()))
    #expect(knob.settings.draft.pages.first { $0.id == "print" }?.on == false)
}

@MainActor
@Test func aKnobWithoutThePrintPageIsLeftAlone() async {
    let server = PrintServer()
    server.pages = #"["bot"]"#
    let knob = await knobModel(server)
    #expect(!(await knob.turnOnPrintPageIfUntouched()))
    #expect(server.puts.isEmpty)
}

@MainActor
@Test func theMarksSurviveARelaunch() {
    let suite = UserDefaults(suiteName: "knob-marks-\(UUID().uuidString)")!
    DefaultsKnobPageMarks(defaults: suite).markDecided(knob: "knob-1", page: "print")
    let again = DefaultsKnobPageMarks(defaults: suite)
    #expect(again.isDecided(knob: "knob-1", page: "print"))
    #expect(!again.isDecided(knob: "knob-2", page: "print"))
}
