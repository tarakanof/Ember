import Testing
import Foundation
@testable import EmberKit

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

@Test func usageConfigThresholdDecodesWithOlderServerDefault() throws {
    // Older server: no usage_threshold_pct in the body -> default 60.
    let old = #"{"usage_widget":true,"usage_per_model":false,"limit_alarm":true}"#
    let cfg = try JSONDecoder().decode(UsageConfig.self, from: Data(old.utf8))
    #expect(cfg.usageThresholdPct == 60)

    let new = #"{"usage_widget":true,"usage_per_model":false,"limit_alarm":true,"usage_threshold_pct":75}"#
    let cfg2 = try JSONDecoder().decode(UsageConfig.self, from: Data(new.utf8))
    #expect(cfg2.usageThresholdPct == 75)
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

@Test func testMeetingsConfigDecodesFull() throws {
    let json = #"{"enabled":false,"tile_lead_minutes":30,"popup_lead_minutes":5,"chime":false,"ics_urls_configured":2}"#
    let cfg = try JSONDecoder().decode(MeetingsConfig.self, from: Data(json.utf8))
    #expect(cfg.enabled == false)
    #expect(cfg.tileLeadMinutes == 30)
    #expect(cfg.popupLeadMinutes == 5)
    #expect(cfg.chime == false)
    #expect(cfg.icsUrlsConfigured == 2)
}

@Test func testMeetingsConfigDecodesPartial() throws {
    let json = #"{}"#
    let cfg = try JSONDecoder().decode(MeetingsConfig.self, from: Data(json.utf8))
    #expect(cfg.enabled == true)
    #expect(cfg.tileLeadMinutes == 60)
    #expect(cfg.popupLeadMinutes == 2)
    #expect(cfg.chime == true)
    #expect(cfg.icsUrlsConfigured == 0)
}

@Test func testMeetingsConfigEncodesSnakeCase() throws {
    let cfg = MeetingsConfig(enabled: true, tileLeadMinutes: 60, popupLeadMinutes: 2, chime: true, icsUrlsConfigured: 3)
    let data = try JSONEncoder().encode(cfg)
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["enabled"] != nil)
    #expect(obj["tile_lead_minutes"] != nil)
    #expect(obj["popup_lead_minutes"] != nil)
    #expect(obj["chime"] != nil)
    // round-trip
    let dec = JSONDecoder()
    let cfg2 = try dec.decode(MeetingsConfig.self, from: data)
    #expect(cfg2 == cfg)
}

@Test func testMeetingsStateDecodes() throws {
    let json = #"{"upcoming":[{"title":"STANDUP","start":"2026-06-12T09:30:00Z"}],"fetched_at":"2026-06-12T09:00:00Z"}"#
    let dec = JSONDecoder(); dec.dateDecodingStrategy = .iso8601
    let state = try dec.decode(MeetingsState.self, from: Data(json.utf8))
    #expect(state.upcoming.count == 1)
    #expect(state.upcoming[0].title == "STANDUP")
    // start must decode as a Date (non-zero interval since 1970)
    #expect(state.upcoming[0].start.timeIntervalSince1970 > 0)
    #expect(state.fetchedAt != nil)
}

@Test func testMeetingsStateDecodesEmpty() throws {
    let json = #"{"upcoming":[]}"#
    let dec = JSONDecoder(); dec.dateDecodingStrategy = .iso8601
    let state = try dec.decode(MeetingsState.self, from: Data(json.utf8))
    #expect(state.upcoming.isEmpty)
    #expect(state.fetchedAt == nil)
}

@Test func quietConfigRoundTripAndDefaults() throws {
    let def = QuietConfig()
    #expect(!def.enabled)
    #expect(def.start == "22:00")
    #expect(def.end == "08:00")

    let cfg = QuietConfig(enabled: true, start: "23:30", end: "07:15")
    let data = try JSONEncoder().encode(cfg)
    let obj = try JSONSerialization.jsonObject(with: data) as! [String: Any]
    #expect(obj["enabled"] as? Bool == true)
    #expect(obj["start"] as? String == "23:30")
    #expect(obj["end"] as? String == "07:15")
    #expect(try JSONDecoder().decode(QuietConfig.self, from: data) == cfg)
}
