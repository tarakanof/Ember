import Foundation

public struct DraftDisplay: Sendable, Equatable {
    public var contextPct = false
    public var activityDetail = false
    public var rateBottomBar = false
    public var sourceCard = true
    public var sessionBar = true
    public var usageCard = true
    public var sourceColor = ""
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

public struct WeatherPreviewDraft: Sendable, Equatable {
    public var rotateInApps: Bool
    public var forecastTile: Bool
    public var airTile: Bool
    public var forecastHours: Int
    public var units: String
    public var moonPhase: Bool
    public var latitude: Double
    public var longitude: Double

    public init(_ cfg: WeatherConfig) {
        rotateInApps = cfg.rotateInApps
        forecastTile = cfg.forecastTile
        airTile = cfg.airTile
        forecastHours = cfg.forecastHours
        units = cfg.units
        moonPhase = cfg.moonPhase
        latitude = cfg.latitude
        longitude = cfg.longitude
    }

    var queryItems: [URLQueryItem] {
        [
            URLQueryItem(name: "rotate_in_apps", value: rotateInApps ? "true" : "false"),
            URLQueryItem(name: "forecast_tile", value: forecastTile ? "true" : "false"),
            URLQueryItem(name: "air_tile", value: airTile ? "true" : "false"),
            URLQueryItem(name: "forecast_hours", value: String(forecastHours)),
            URLQueryItem(name: "units", value: units),
            URLQueryItem(name: "moon_phase", value: moonPhase ? "true" : "false"),
            URLQueryItem(name: "lat", value: String(latitude)),
            URLQueryItem(name: "lon", value: String(longitude)),
        ]
    }
}

public struct PreviewService: Sendable {
    let client: APIClient
    public init(client: APIClient) { self.client = client }
    public func fetchPreview(_ draft: DraftDisplay) async throws -> PreviewResponse {
        try await client.get("/v1/preview", query: draft.queryItems)
    }

    public func fetchWeatherPreview(_ draft: WeatherPreviewDraft) async throws -> PreviewResponse {
        try await client.get("/v1/weather/preview", query: draft.queryItems)
    }

    public func fetchPomodoroPreview(_ cfg: PomoConfig) async throws -> PreviewResponse {
        try await client.get("/v1/pomodoro/preview", query: [
            URLQueryItem(name: "focus_minutes", value: String(cfg.focusMinutes)),
            URLQueryItem(name: "short_break_minutes", value: String(cfg.shortBreakMinutes)),
            URLQueryItem(name: "long_break_minutes", value: String(cfg.longBreakMinutes)),
            URLQueryItem(name: "focus_color", value: cfg.focusColor),
            URLQueryItem(name: "break_color", value: cfg.breakColor),
        ])
    }

    public func fetchReminderPreview() async throws -> PreviewResponse {
        try await client.get("/v1/reminders/preview", query: [])
    }

    public func fetchMeetingsPreview() async throws -> PreviewResponse {
        try await client.get("/v1/meetings/preview", query: [])
    }
}
