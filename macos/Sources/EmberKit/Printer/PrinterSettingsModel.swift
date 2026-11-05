import Foundation
import Observation

@MainActor
@Observable
public final class PrinterSettingsModel {
    public let config: ServerConfigModel<PrinterConfig>
    public let preview: PreviewModel
    public private(set) var state: PrinterState?
    public private(set) var stateError: FeedError?

    @ObservationIgnored private let fetchState: @Sendable () async throws -> PrinterState
    @ObservationIgnored private let fetchPreview: @Sendable () async throws -> PreviewResponse

    public var status: PrinterStatusLine { PrinterStatusLine(state) }

    public var foundHost: String? {
        guard let state, state.printer.discovered, !state.printer.host.isEmpty else { return nil }
        return state.printer.host
    }

    public init(client: APIClient) {
        config = ServerConfigModel(initial: PrinterConfig(),
                                   load: { try await client.get("/v1/print/config") },
                                   save: { try await client.put("/v1/print/config", body: $0) })
        preview = PreviewModel()
        fetchState = { try await client.get("/v1/print/state") }
        fetchPreview = { try await client.get("/v1/print/preview") }
    }

    init(config: ServerConfigModel<PrinterConfig>,
         state: @escaping @Sendable () async throws -> PrinterState,
         preview: @escaping @Sendable () async throws -> PreviewResponse,
         previewModel: PreviewModel = PreviewModel()) {
        self.config = config
        self.preview = previewModel
        fetchState = state
        fetchPreview = preview
    }

    public func refreshState() async {
        do {
            state = try await fetchState()
            stateError = nil
        } catch {
            state = nil
            stateError = FeedError(error)
        }
    }

    public func refreshPreview() {
        preview.request { [fetchPreview] in try await fetchPreview() }
    }

    public func load() async {
        async let c: Void = config.load()
        async let s: Void = refreshState()
        _ = await (c, s)
    }

    public func saved() async {
        await refreshState()
        refreshPreview()
    }
}
