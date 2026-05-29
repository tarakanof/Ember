import Testing
import Foundation
@testable import AwtrixMenuKit

@Test func decodesPomoState() throws {
    let json = #"{"phase":"focus","running":true,"paused":false,"remaining_sec":1500,"planned_sec":1500,"round":2}"#
    let s = try JSONDecoder().decode(PomoState.self, from: Data(json.utf8))
    #expect(s.phase == "focus")
    #expect(s.running)
    #expect(s.remainingSec == 1500)
    #expect(s.round == 2)
}

@Test func decodesPomoStats() throws {
    let json = #"{"today":{"date":"2026-05-29","completed_focus":3,"focus_min":75},"history":[],"streak":4}"#
    let s = try JSONDecoder().decode(PomoStats.self, from: Data(json.utf8))
    #expect(s.today.completedFocus == 3)
    #expect(s.today.focusMin == 75)
    #expect(s.streak == 4)
}

@Test func decodesSessionWithOptionalPointers() throws {
    let json = #"{"source":"mbp","tool":"claude","session":"s1","state":"running","message":"","context_pct":42,"context_number":true,"updated_at":"2026-05-29T12:00:00Z"}"#
    let dec = JSONDecoder(); dec.dateDecodingStrategy = .iso8601
    let s = try dec.decode(Session.self, from: Data(json.utf8))
    #expect(s.source == "mbp")
    #expect(s.contextPct == 42)
    #expect(s.rateWindowPct == nil)
    #expect(s.contextNumber)
    #expect(s.activity == "")
}

@Test func decodesPreviewResponse() throws {
    let px = Array(repeating: "#000000", count: 256)
    let pxJSON = "[" + px.map { "\"\($0)\"" }.joined(separator: ",") + "]"
    let json = #"{"width":32,"height":8,"activity":"Bash: go test","frames":[{"card":"xy","pixels":\#(pxJSON)}]}"#
    let p = try JSONDecoder().decode(PreviewResponse.self, from: Data(json.utf8))
    #expect(p.width == 32)
    #expect(p.frames.count == 1)
    #expect(p.frames[0].card == "xy")
    #expect(p.frames[0].pixels.count == 256)
    #expect(p.activity == "Bash: go test")
}
