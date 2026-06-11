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
}
