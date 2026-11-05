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
    @ObservationIgnored private var failures = 0

    static let failuresBeforeUnavailable = 3

    public var status: PrinterStatusLine {
        if config.draft.enabled, config.saveError == nil, state?.enabled == false { return .unknown }
        return PrinterStatusLine(state, error: stateError)
    }

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
        addressInvalid = false
        if !config.draft.isAuto { addressText = config.draft.url }
        commitAddress(warn: false)
    }

    public func commitAddress(warn: Bool = true) {
        guard manualMode else { return }
        let text = addressText.trimmingCharacters(in: .whitespacesAndNewlines)
        if text.isEmpty {
            addressInvalid = false
            if !config.draft.isAuto { addressText = config.draft.url }
        } else if let url = PrinterConfig.validatedAddress(text) {
            addressInvalid = false
            addressText = url
            if config.draft.url != url {
                config.draft.url = url
                config.scheduleSave()
            }
        } else {
            addressInvalid = warn
        }
    }

    public func refreshState() async {
        stateSeq += 1
        let seq = stateSeq
        do {
            let next = try await fetchState()
            guard seq == stateSeq else { return }
            failures = 0
            state = next
            stateError = nil
        } catch {
            guard seq == stateSeq else { return }
            let e = FeedError(error)
            failures += 1
            if state == nil || failures >= Self.failuresBeforeUnavailable || e == .featureOff || e == .unauthorized {
                state = nil
                stateError = e
            }
        }
    }

    public var previewResponse: PreviewResponse? {
        guard config.draft.enabled, config.draft.clockApp, !previewMissing else { return nil }
        return preview.response
    }

    public var previewMissing: Bool { preview.error == .featureOff }

    public func refreshPreview() {
        guard config.draft.enabled, config.draft.clockApp else { return }
        preview.request { [fetchPreview] in try await fetchPreview() }
    }

    public func load() async {
        let before = config.applied
        async let c: Void = config.load()
        async let s: Void = refreshState()
        _ = await (c, s)
        if before == nil || config.applied?.url != before?.url, !config.hasUnsavedChanges { syncAddress() }
    }

    public func saved() async {
        await refreshState()
        refreshPreview()
    }
}

@MainActor
public final class PrinterModelCache {
    public static let shared = PrinterModelCache()

    private var key: Key?
    private var model: PrinterSettingsModel?

    private struct Key: Equatable {
        let baseURL: URL?
        let token: String?
    }

    public init() {}

    public func model(for client: APIClient) -> PrinterSettingsModel {
        let next = Key(baseURL: client.baseURL, token: client.token)
        if let model, key == next { return model }
        let fresh = PrinterSettingsModel(client: client)
        key = next
        model = fresh
        return fresh
    }
}
