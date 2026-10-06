import Foundation
import Observation

/// Anything with a save status, for `AggregateSaveStatus`.
@MainActor
public protocol SaveStatusReporting: AnyObject {
    var status: SaveState { get }
}

/// One editable configuration with auto-apply.
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
    /// Why the last save failed; nil after a success.
    public private(set) var saveError: FeedError?

    public var isLoaded: Bool { applied != nil }
    public var hasUnsavedChanges: Bool { applied.map { $0 != draft } ?? false }

    /// Called on the main actor after each successful save with the saved
    /// value and the one stored before it (a Connection save rebuilds the
    /// client).
    @ObservationIgnored public var onSaved: (@MainActor (_ saved: T, _ previous: T?) -> Void)?

    @ObservationIgnored private let loader: @Sendable () async throws -> T
    @ObservationIgnored private let saver: @Sendable (T, T?) async throws -> Void
    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let savedHold: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var pending: Task<Void, Never>?
    @ObservationIgnored private var savedReset: Task<Void, Never>?
    @ObservationIgnored private var saving = false

    static var rateLimitAttempts: Int { 3 }

    public convenience init(initial: T,
                            load: @escaping @Sendable () async throws -> T,
                            save: @escaping @Sendable (T) async throws -> Void,
                            debounce: Duration = .milliseconds(600)) {
        self.init(initial: initial, load: load, save: save, debounce: debounce,
                  savedHold: .seconds(2), sleep: { try await Task.sleep(for: $0) })
    }

    convenience init(initial: T,
                     load: @escaping @Sendable () async throws -> T,
                     save: @escaping @Sendable (T) async throws -> Void,
                     debounce: Duration,
                     savedHold: Duration,
                     sleep: @escaping @Sendable (Duration) async throws -> Void) {
        self.init(initial: initial, load: load, saveChange: { value, _ in try await save(value) },
                  debounce: debounce, savedHold: savedHold, sleep: sleep)
    }

    init(initial: T,
         load: @escaping @Sendable () async throws -> T,
         saveChange save: @escaping @Sendable (T, T?) async throws -> Void,
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

    /// Reads the stored value into `draft` and `applied`.
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

    /// Saves `draft` after the debounce, restarting it on every call.
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
        let previous = applied
        savedReset?.cancel()
        status = .saving
        for attempt in 1...Self.rateLimitAttempts {
            do {
                try await saver(sent, previous)
                applied = sent
                saveError = nil
                status = .saved
                onSaved?(sent, previous)
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
        if hasUnsavedChanges { scheduleSave() }
    }

    /// Replaces the draft and applied value without saving.
    public func reset(to value: T) {
        cancelPendingSave()
        applied = value
        draft = value
    }

    /// Retries the last failed save now.
    public func retry() async { await saveNow() }

    /// Drops unsaved edits and a save error: the draft goes back to the last
    /// stored value.
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
    /// nothing).
    public convenience init(env store: EnvFileStore,
                            initial: T,
                            read: @escaping @Sendable (EnvFile) -> T,
                            apply: @escaping @Sendable (T, inout EnvFile) throws -> Void,
                            debounce: Duration = .milliseconds(600)) {
        self.init(env: store, initial: initial, read: read,
                  applyChange: { value, _, env in try apply(value, &env) }, debounce: debounce)
    }

    /// A model over producer.env whose `applyChange` also gets the value last
    /// loaded or saved, so it can write only what the user changed.
    public convenience init(env store: EnvFileStore,
                            initial: T,
                            read: @escaping @Sendable (EnvFile) -> T,
                            applyChange: @escaping @Sendable (T, T?, inout EnvFile) throws -> Void,
                            debounce: Duration = .milliseconds(600)) {
        self.init(initial: initial,
                  load: { read(await store.read()) },
                  saveChange: { value, previous in try await store.update { try applyChange(value, previous, &$0) } },
                  debounce: debounce, savedHold: .seconds(2), sleep: { try await Task.sleep(for: $0) })
    }
}

extension FeedError {
    /// The inline error under a settings section.
    public var saveMessage: LocalizedStringResource {
        switch self {
        case .offline: "Server unreachable"
        case .timedOut, .clockTimedOut: message
        case .localNetworkDenied: "Local Network access is off for Ember"
        case .unauthorized: "Unauthorized — check the token in Connection."
        case .rateLimited: "The server is rate-limiting this Mac. Try again in a moment."
        case .featureOff: "This server doesn't support this setting. Update the server."
        case .server(let message): "Server error: \(message)"
        }
    }
}
