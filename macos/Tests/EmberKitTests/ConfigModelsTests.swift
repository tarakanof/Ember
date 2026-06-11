import Testing
import Foundation
@testable import EmberKit

@Test func displayConfigCodingKeys() throws {
    let json = #"{"idle_hide_minutes":2,"attention_hold_seconds":30,"attention_chime":false}"#
    let cfg = try JSONDecoder().decode(DisplayConfig.self, from: Data(json.utf8))
    #expect(cfg.idleHideMinutes == 2)
    #expect(cfg.attentionHoldSeconds == 30)
    #expect(cfg.attentionChime == false)
    // encode side: keys are snake_case
    let data = try JSONEncoder().encode(cfg)
    let s = String(decoding: data, as: UTF8.self)
    #expect(s.contains("\"idle_hide_minutes\""))
}

@Test func usageConfigLimitAlarmDefaultsTrueWhenAbsent() throws {
    let old = #"{"usage_widget":true,"usage_per_model":false}"#
    let cfg = try JSONDecoder().decode(UsageConfig.self, from: Data(old.utf8))
    #expect(cfg.limitAlarm)
    #expect(cfg.usageWidget)
    #expect(!cfg.usagePerModel)
}

@Test func usageConfigLimitAlarmRoundTrips() throws {
    var cfg = UsageConfig()
    cfg.limitAlarm = false
    let data = try JSONEncoder().encode(cfg)
    let back = try JSONDecoder().decode(UsageConfig.self, from: data)
    #expect(!back.limitAlarm)
}
