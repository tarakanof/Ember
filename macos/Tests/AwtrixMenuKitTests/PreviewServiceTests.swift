import Testing
import Foundation
@testable import AwtrixMenuKit

@Test func buildsQueryFromDraftAndDecodes() async throws {
    var draft = DraftDisplay()
    draft.contextPct = true
    draft.contextNumber = true
    draft.sourceColor = "#ff8800"
    let client = stubbedClient { req in
        #expect(req.url?.path == "/v1/preview")
        let items = URLComponents(url: req.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let q = Dictionary(uniqueKeysWithValues: items.map { ($0.name, $0.value ?? "") })
        #expect(q["context_pct"] == "true")
        #expect(q["context_number"] == "true")
        #expect(q["rate_pct"] == "false")
        #expect(q["source_color"] == "#ff8800")
        #expect(q["activity_trail"] == nil)
        let px = "[" + Array(repeating: "\"#000000\"", count: 256).joined(separator: ",") + "]"
        return (okResponse(req.url!), Data(#"{"width":32,"height":8,"activity":"","frames":[{"card":"xy","pixels":\#(px)}]}"#.utf8))
    }
    let svc = PreviewService(client: client)
    let p = try await svc.fetchPreview(draft)
    #expect(p.frames.first?.card == "xy")
    #expect(p.frames.first?.pixels.count == 256)
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
