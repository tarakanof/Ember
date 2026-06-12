import Testing
import Foundation
@testable import EmberKit

@Test func buildsQueryFromDraftAndDecodes() async throws {
    var draft = DraftDisplay()
    draft.contextPct = true
    draft.rateBottomBar = true
    draft.sourceColor = "#ff8800"
    let client = stubbedClient { req in
        #expect(req.url?.path == "/v1/preview")
        let items = URLComponents(url: req.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let q = Dictionary(uniqueKeysWithValues: items.map { ($0.name, $0.value ?? "") })
        #expect(q["context_pct"] == "true")
        #expect(q["rate_bottom_bar"] == "true")
        #expect(q["activity_detail"] == "false")
        #expect(q["source_card"] == "true")
        #expect(q["session_bar"] == "true")
        #expect(q["usage_card"] == "true")
        #expect(q["source_color"] == "#ff8800")
        #expect(q["activity_trail"] == nil)
        #expect(q["rate_pct"] == nil)
        #expect(q["context_number"] == nil)
        #expect(q["rate_reset"] == nil)
        let px = "[" + Array(repeating: "\"#000000\"", count: 256).joined(separator: ",") + "]"
        return (okResponse(req.url!), Data(#"{"width":32,"height":8,"activity":"","frames":[{"card":"xy","pixels":\#(px)}]}"#.utf8))
    }
    let svc = PreviewService(client: client)
    let p = try await svc.fetchPreview(draft)
    #expect(p.frames.first?.card == "xy")
    #expect(p.frames.first?.pixels.count == 256)
}

@Test func weatherPreviewSendsAirTileFlag() async throws {
    var cfg = WeatherConfig(enabled: true, latitude: 1, longitude: 2)
    cfg.airTile = false
    let client = stubbedClient { req in
        #expect(req.url?.path == "/v1/weather/preview")
        let items = URLComponents(url: req.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let q = Dictionary(uniqueKeysWithValues: items.map { ($0.name, $0.value ?? "") })
        #expect(q["air_tile"] == "false")
        let px = "[" + Array(repeating: "\"#000000\"", count: 256).joined(separator: ",") + "]"
        return (okResponse(req.url!), Data(#"{"width":32,"height":8,"activity":"","frames":[{"card":"weather","pixels":\#(px)}]}"#.utf8))
    }
    let p = try await PreviewService(client: client).fetchWeatherPreview(cfg)
    #expect(p.frames.first?.card == "weather")
}

@Test func pomodoroPreviewSendsDraftConfig() async throws {
    let cfg = PomoConfig(focusMinutes: 50, shortBreakMinutes: 10, longBreakMinutes: 20,
                         roundsBeforeLongBreak: 4, autoStartNext: false, sound: true,
                         soundMelody: "", focusColor: "#ff00ff", breakColor: "#00ffff")
    let client = stubbedClient { req in
        #expect(req.url?.path == "/v1/pomodoro/preview")
        let items = URLComponents(url: req.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let q = Dictionary(uniqueKeysWithValues: items.map { ($0.name, $0.value ?? "") })
        #expect(q["focus_minutes"] == "50")
        #expect(q["short_break_minutes"] == "10")
        #expect(q["long_break_minutes"] == "20")
        #expect(q["focus_color"] == "#ff00ff")
        #expect(q["break_color"] == "#00ffff")
        let px = "[" + Array(repeating: "\"#000000\"", count: 256).joined(separator: ",") + "]"
        return (okResponse(req.url!), Data(#"{"width":32,"height":8,"activity":"","frames":[{"card":"focus","pixels":\#(px)}]}"#.utf8))
    }
    let p = try await PreviewService(client: client).fetchPomodoroPreview(cfg)
    #expect(p.frames.first?.card == "focus")
}

@Test func reminderPreviewHitsEndpoint() async throws {
    let client = stubbedClient { req in
        #expect(req.url?.path == "/v1/reminders/preview")
        let px = "[" + Array(repeating: "\"#000000\"", count: 256).joined(separator: ",") + "]"
        return (okResponse(req.url!), Data(#"{"width":32,"height":8,"activity":"","frames":[{"card":"reminder","pixels":\#(px)}]}"#.utf8))
    }
    let p = try await PreviewService(client: client).fetchReminderPreview()
    #expect(p.frames.first?.card == "reminder")
}

@Test func emptySourceColorOmitsParam() async throws {
    let client = stubbedClient { req in
        let items = URLComponents(url: req.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        #expect(!items.contains { $0.name == "source_color" })
        let px = "[" + Array(repeating: "\"#000000\"", count: 256).joined(separator: ",") + "]"
        return (okResponse(req.url!), Data(#"{"width":32,"height":8,"activity":"","frames":[{"card":"xy","pixels":\#(px)}]}"#.utf8))
    }
    _ = try await PreviewService(client: client).fetchPreview(DraftDisplay())
}
