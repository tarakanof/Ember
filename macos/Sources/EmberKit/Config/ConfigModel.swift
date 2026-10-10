import Foundation
import Observation

@MainActor
public protocol SaveStatusReporting: AnyObject {
    var status: SaveState { get }
}

@MainActor
@Observable
public final class ConfigModel<T: Equatable & Sendable>: SaveStatusReporting {
    public var draft: T
    public private(set) var applied: T?
    public private(set) var status: SaveState = .idle
    public private(set) var loadError: FeedError?
    public private(set) var saveError: FeedError?

    public var isLoaded: Bool { applied != nil }
    public var hasUnsavedChanges: Bool { applied.map { $0 != draft } ?? false }

    /// Called on the main actor after each successful save.
    @ObservationIgnored public var onSaved: (@MainActor (_ saved: T, _ previous: T?) -> Void)?

    @ObservationIgnored private let loader: @Sendable () async throws -> T
    @ObservationIgnored private let saver: @Sendable (T, T?) async throws -> Void
    @ObservationIgnored private let debounce: Duration
    @ObservationIgnored private let savedHold: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private var pending: Task<Void, Never>?
    @ObservationIgnored private var savedReset: Task<Void, Never>?
    @ObservationIgnored private var saving = false
    @ObservationIgnored private var loadSeq = 0
    @ObservationIgnored private(set) var saveEpoch = 0
    var isSaving: Bool { saving }
    var hasPendingSave: Bool { pending != nil }

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

    public func load() async {
        await fetch()
    }

    @discardableResult
    func fetch() async -> Bool {
        if let e = saveError, hasUnsavedChanges, pending == nil, !saving {
            if e == .featureOff { revert() } else { await saveNow(); return false }
        }
        guard pending == nil, !saving, !hasUnsavedChanges else { return false }
        loadSeq += 1
        let seq = loadSeq
        for attempt in 1...Self.rateLimitAttempts {
            do {
                let value = try await loader()
                loadError = nil
                guard seq == loadSeq || applied == nil, pending == nil, !saving, !hasUnsavedChanges else { return false }
                applied = value
                draft = value
                loadError = nil
                return true
            } catch {
                guard seq == loadSeq else { return false }
                let e = FeedError(error)
                if e == .rateLimited, attempt < Self.rateLimitAttempts {
                    try? await sleep((error as? APIError)?.retryAfter ?? RateLimitBackoff.fallbackRetryAfter)
                    continue
                }
                loadError = e
                return false
            }
        }
        return false
    }

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

    public func saveNow() async {
        pending?.cancel()
        pending = nil
        guard hasUnsavedChanges, !saving else { return }
        saving = true
        defer { saving = false }
        loadSeq += 1
        saveEpoch += 1
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
            } catch let recovered as SaveRecovered<T> {
                cancelPendingSave()
                let newer = draft
                applied = recovered.current
                draft = newer == sent ? recovered.current : recovered.rebase(sent, newer)
                saveError = recovered.cause
                status = .error(String(localized: recovered.cause.saveMessage))
                if hasUnsavedChanges { scheduleSave() }
                return
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

    func amend(overlapped: Bool, _ change: (inout T) -> Void) {
        loadSeq += 1
        change(&draft)
        if !overlapped, var a = applied {
            change(&a)
            applied = a
        }
        if hasUnsavedChanges, !saving { scheduleSave() }
    }

    public func reset(to value: T) {
        cancelPendingSave()
        applied = value
        draft = value
    }

    public func retry() async { await saveNow() }

    public func revert() {
        cancelPendingSave()
        if let applied { draft = applied }
        saveError = nil
        if case .error = status { status = .idle }
    }

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

struct SaveRecovered<T: Sendable>: Error {
    let current: T
    let cause: FeedError
    let rebase: @Sendable (_ sent: T, _ draft: T) -> T
}

public typealias ServerConfigModel<T: Equatable & Sendable> = ConfigModel<T>

public typealias EnvConfigModel<T: Equatable & Sendable> = ConfigModel<T>

extension ConfigModel {
    public convenience init(env store: EnvFileStore,
                            initial: T,
                            read: @escaping @Sendable (EnvFile) -> T,
                            apply: @escaping @Sendable (T, inout EnvFile) throws -> Void,
                            debounce: Duration = .milliseconds(600)) {
        self.init(env: store, initial: initial, read: read,
                  applyChange: { value, _, env in try apply(value, &env) }, debounce: debounce)
    }

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
    public var saveMessage: LocalizedStringResource {
        switch self {
        case .offline: "Server unreachable"
        case .timedOut, .clockTimedOut: message
        case .localNetworkDenied: "Local Network access is off for Ember"
        case .unauthorized: "Unauthorized — check the token in Connection."
        case .rateLimited: "The server is rate-limiting this Mac. Try again in a moment."
        case .featureOff: "This server doesn't support this setting. Update the server."
        case .server(let message): "Server error: \(message)"
        case .rejected(let reason): reason
        }
    }
}
