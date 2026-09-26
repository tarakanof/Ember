import Foundation
import Observation

/// Anything with a save status, for `AggregateSaveStatus`.
@MainActor
public protocol SaveStatusReporting: AnyObject {
    var status: SaveState { get }
}

/// One editable configuration with auto-apply. Views bind controls to
/// `draft` and call `scheduleSave()` from `onChange(of: draft)`; the model
/// writes 600 ms after the last edit, retries 429s, and keeps the outcome in
/// `status` ("Saved" for 2 s, then idle). It never writes before a load has
/// succeeded, so a control can't overwrite the server with the placeholder:
/// **views must disable their controls until `isLoaded`**, or an edit made
/// before the first load is silently discarded.
///
/// A failed save is retried by the next `load()` (pane appear, window focus,
/// ⌘R); a `.featureOff` save is not retried but reverted to the server's
/// value, since the server doesn't have the setting.
///
/// Server configs use it as `ServerConfigModel<T>`, producer.env settings as
/// `EnvConfigModel<T>` (see `init(envAt:…)`); the interface is the same.
@MainActor
@Observable
public final class ConfigModel<T: Equatable & Sendable>: SaveStatusReporting {
    /// What the controls show and edit.
    public var draft: T
    /// The last value loaded from or saved to the store; nil before the
    /// first successful load.
    public private(set) var applied: T?
    public private(set) var status: SaveState = .idle
    /// Why the last load failed; nil after a success.
    public private(set) var loadError: FeedError?
    /// Why the last save failed; nil after a success. `.featureOff` means the
    /// server doesn't have this setting: disable its controls.
    public private(set) var saveError: FeedError?

    public var isLoaded: Bool { applied != nil }
    public var hasUnsavedChanges: Bool { applied.map { $0 != draft } ?? false }

    /// Called on the main actor after each successful save (a Connection save
    /// rebuilds the client).
    @ObservationIgnored public var onSaved: (@MainActor (T) -> Void)?

    @ObservationIgnored private let loader: @Sendable () async throws -> T
    @ObservationIgnored private let saver: @Sendable (T) async throws -> Void
    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let savedHold: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var pending: Task<Void, Never>?
    @ObservationIgnored private var savedReset: Task<Void, Never>?
    @ObservationIgnored private var saving = false

    /// Attempts per load or save while the server answers 429.
    static var rateLimitAttempts: Int { 3 }

    public convenience init(initial: T,
                            load: @escaping @Sendable () async throws -> T,
                            save: @escaping @Sendable (T) async throws -> Void,
                            debounce: Duration = .milliseconds(600)) {
        self.init(initial: initial, load: load, save: save, debounce: debounce,
                  savedHold: .seconds(2), sleep: { try await Task.sleep(for: $0) })
    }

    /// Tests inject the sleep so the debounce and retries run on a manual clock.
    init(initial: T,
         load: @escaping @Sendable () async throws -> T,
         save: @escaping @Sendable (T) async throws -> Void,
         debounce: Duration,
         savedHold: Duration,
         sleep: @escaping @Sendable (Duration) async throws -> Void) {
        draft = initial
        loader = load
        saver = save
        self.debounce = debounce
        self.savedHold = savedHold
        self.sleep = sleep
    }

    /// Reads the stored value into `draft` and `applied`. Skipped while an
    /// edit is waiting to be saved, so a reload (window focus, ⌘R) can't
    /// throw away what the user just typed. After a failed save it retries
    /// that save instead (or reverts, for `.featureOff`).
    public func load() async {
        if let e = saveError, hasUnsavedChanges, pending == nil, !saving {
            if e == .featureOff { revert() } else { await saveNow(); return }
        }
        guard pending == nil, !saving, !hasUnsavedChanges else { return }
        for attempt in 1...Self.rateLimitAttempts {
            do {
                let value = try await loader()
                guard pending == nil, !saving, !hasUnsavedChanges else { return }
                applied = value
                draft = value
                loadError = nil
                return
            } catch {
                let e = FeedError(error)
                if e == .rateLimited, attempt < Self.rateLimitAttempts {
                    try? await sleep((error as? APIError)?.retryAfter ?? RateLimitBackoff.fallbackRetryAfter)
                    continue
                }
                loadError = e
                return
            }
        }
    }

    /// Saves `draft` after the debounce, restarting it on every call. Does
    /// nothing before the first load or when nothing changed.
    public func scheduleSave() {
        pending?.cancel()
        pending = nil
        guard hasUnsavedChanges else { return }
        let wait = debounce
        pending = Task { [weak self] in
            do { try await self?.sleep(wait) } catch { return }
            guard let self, !Task.isCancelled else { return }
            self.pending = nil
            await self.saveNow()
        }
    }

    /// Saves `draft` now, cancelling a pending debounce.
    public func saveNow() async {
        pending?.cancel()
        pending = nil
        guard hasUnsavedChanges, !saving else { return }
        saving = true
        defer { saving = false }
        let sent = draft
        savedReset?.cancel()
        status = .saving
        for attempt in 1...Self.rateLimitAttempts {
            do {
                try await saver(sent)
                applied = sent
                saveError = nil
                status = .saved
                onSaved?(sent)
                holdSaved()
                break
            } catch {
                let e = FeedError(error)
                if e == .rateLimited, attempt < Self.rateLimitAttempts {
                    try? await sleep((error as? APIError)?.retryAfter ?? RateLimitBackoff.fallbackRetryAfter)
                    continue
                }
                saveError = e
                status = .error(String(localized: e.saveMessage))
                return
            }
        }
        // Edits made while the request was in flight get their own save.
        if hasUnsavedChanges { scheduleSave() }
    }

    /// Replaces the draft and applied value without saving (e.g. after a
    /// write made elsewhere).
    public func reset(to value: T) {
        cancelPendingSave()
        applied = value
        draft = value
    }

    /// Retries the last failed save now.
    public func retry() async { await saveNow() }

    /// Drops unsaved edits and a save error: the draft goes back to the last
    /// stored value. Call `load()` afterwards to refetch.
    public func revert() {
        cancelPendingSave()
        if let applied { draft = applied }
        saveError = nil
        if case .error = status { status = .idle }
    }

    /// Drops a debounced save that hasn't started (the server is changing).
    public func cancelPendingSave() {
        pending?.cancel()
        pending = nil
    }

    private func holdSaved() {
        let hold = savedHold
        savedReset = Task { [weak self] in
            do { try await self?.sleep(hold) } catch { return }
            guard let self, !Task.isCancelled, self.status == .saved else { return }
            self.status = .idle
        }
    }
}

/// A config stored on the server (`GET`/`PUT /v1/…/config`).
public typealias ServerConfigModel<T: Equatable & Sendable> = ConfigModel<T>

/// A setting stored in producer.env, shared with the Go producers.
public typealias EnvConfigModel<T: Equatable & Sendable> = ConfigModel<T>

extension ConfigModel {
    /// A model over producer.env: `read` parses the settings out of the file;
    /// `apply` writes them into it (validating first, so a throw writes
    /// nothing). The rest of the file is kept. Writes go through `store`, so
    /// they can't interleave with other writers of the same file.
    public convenience init(env store: EnvFileStore,
                            initial: T,
                            read: @escaping @Sendable (EnvFile) -> T,
                            apply: @escaping @Sendable (T, inout EnvFile) throws -> Void,
                            debounce: Duration = .milliseconds(600)) {
        self.init(initial: initial,
                  load: { read(await store.read()) },
                  save: { value in try await store.update { try apply(value, &$0) } },
                  debounce: debounce)
    }
}

extension FeedError {
    /// The inline error under a settings section.
    public var saveMessage: LocalizedStringResource {
        switch self {
        case .offline: "Server unreachable"
        case .unauthorized: "Unauthorized — check the token in Connection."
        case .rateLimited: "The server is rate-limiting this Mac. Try again in a moment."
        case .featureOff: "This server doesn't support this setting. Update the server."
        case .server(let message): "Server error: \(message)"
        }
    }
}
