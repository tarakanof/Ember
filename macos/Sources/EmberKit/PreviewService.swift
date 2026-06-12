import Foundation

/// The six display toggles that affect a single-session render, plus usageCard
/// and an optional source colour. Maps 1:1 to GET /v1/preview query params.
/// activity_trail is deliberately absent (it has no single-session effect; see the spec).
public struct DraftDisplay: Sendable, Equatable {
    public var contextPct = false
    public var activityDetail = false
    public var rateBottomBar = false
    public var sourceCard = true
    public var sessionBar = true
    public var usageCard = true
    public var sourceColor = ""   // "" = omit param
    public init() {}

    var queryItems: [URLQueryItem] {
        var items = [
            URLQueryItem(name: "context_pct", value: contextPct ? "true" : "false"),
            URLQueryItem(name: "activity_detail", value: activityDetail ? "true" : "false"),
            URLQueryItem(name: "rate_bottom_bar", value: rateBottomBar ? "true" : "false"),
            URLQueryItem(name: "source_card", value: sourceCard ? "true" : "false"),
            URLQueryItem(name: "session_bar", value: sessionBar ? "true" : "false"),
            URLQueryItem(name: "usage_card", value: usageCard ? "true" : "false"),
        ]
        if !sourceColor.isEmpty {
            items.append(URLQueryItem(name: "source_color", value: sourceColor))
        }
        return items
    }
}

public struct PreviewService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }
    public func fetchPreview(_ draft: DraftDisplay) async throws -> PreviewResponse {
        try await client.get("/v1/preview", query: draft.queryItems)
    }

    /// Weather-tab preview: GET /v1/weather/preview with the draft weather
    /// config's display-relevant fields. Same response shape as /v1/preview.
    public func fetchWeatherPreview(_ cfg: WeatherConfig) async throws -> PreviewResponse {
        try await client.get("/v1/weather/preview", query: [
            URLQueryItem(name: "rotate_in_apps", value: cfg.rotateInApps ? "true" : "false"),
            URLQueryItem(name: "forecast_tile", value: cfg.forecastTile ? "true" : "false"),
            URLQueryItem(name: "forecast_hours", value: String(cfg.forecastHours)),
            URLQueryItem(name: "units", value: cfg.units),
        ])
    }

    /// Pomodoro-tab preview: GET /v1/pomodoro/preview with the draft config's
    /// display-relevant fields. Returns one frame per phase
    /// (focus / short_break / long_break).
    public func fetchPomodoroPreview(_ cfg: PomoConfig) async throws -> PreviewResponse {
        try await client.get("/v1/pomodoro/preview", query: [
            URLQueryItem(name: "focus_minutes", value: String(cfg.focusMinutes)),
            URLQueryItem(name: "short_break_minutes", value: String(cfg.shortBreakMinutes)),
            URLQueryItem(name: "long_break_minutes", value: String(cfg.longBreakMinutes)),
            URLQueryItem(name: "focus_color", value: cfg.focusColor),
            URLQueryItem(name: "break_color", value: cfg.breakColor),
        ])
    }

    /// Reminders-tab preview: GET /v1/reminders/preview — the bell alarm popup
    /// with the server's sample text. One "reminder" frame.
    public func fetchReminderPreview() async throws -> PreviewResponse {
        try await client.get("/v1/reminders/preview", query: [])
    }
}
