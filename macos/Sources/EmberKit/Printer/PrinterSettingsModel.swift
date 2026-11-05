import Foundation
import Observation

@MainActor
@Observable
public final class PrinterSettingsModel {
    public let config: ServerConfigModel<PrinterConfig>
    public let preview: PreviewModel
    public private(set) var state: PrinterState?
    public private(set) var stateError: FeedError?
    public var manualMode = false
    public var addressText = ""
    public private(set) var addressInvalid = false

    @ObservationIgnored private let fetchState: @Sendable () async throws -> PrinterState
    @ObservationIgnored private let fetchPreview: @Sendable () async throws -> PreviewResponse
    @ObservationIgnored private var stateSeq = 0

    public var status: PrinterStatusLine { PrinterStatusLine(state, error: stateError) }

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
        wire()
    }

    init(config: ServerConfigModel<PrinterConfig>,
         state: @escaping @Sendable () async throws -> PrinterState,
         preview: @escaping @Sendable () async throws -> PreviewResponse,
         previewModel: PreviewModel = PreviewModel()) {
        self.config = config
        self.preview = previewModel
        fetchState = state
        fetchPreview = preview
        wire()
    }

    private func wire() {
        config.onSaved = { [weak self] _, _ in
            guard let self else { return }
            Task { await self.saved() }
        }
    }

    public func syncAddress() {
        guard let applied = config.applied else { return }
        manualMode = !applied.isAuto
        addressText = applied.manualURL
        addressInvalid = false
    }

    public func chooseAuto() {
        manualMode = false
        addressInvalid = false
        config.draft.url = PrinterConfig.auto
    }

    public func chooseManual() {
        manualMode = true
        if !config.draft.isAuto { addressText = config.draft.url }
    }

    public func commitAddress() {
        guard manualMode else { return }
        if let url = PrinterConfig.validatedAddress(addressText) {
            addressInvalid = false
            addressText = url
            config.draft.url = url
        } else {
            addressInvalid = true
        }
    }

    public func refreshState() async {
        stateSeq += 1
        let seq = stateSeq
        do {
            let next = try await fetchState()
            guard seq == stateSeq else { return }
            state = next
            stateError = nil
        } catch {
            guard seq == stateSeq else { return }
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
        syncAddress()
    }

    public func saved() async {
        await refreshState()
        refreshPreview()
    }
}
