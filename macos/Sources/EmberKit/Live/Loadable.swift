import Foundation

/// A feed's value and how fresh it is. A failure keeps the last good value so
/// views can show it as stale instead of blanking.
public enum Loadable<T: Sendable & Equatable>: Equatable, Sendable {
    /// Never loaded (or reset by a server change).
    case loading
    /// `at` is when this value was first fetched: a poll returning the same
    /// value doesn't change it (see `LiveModel.lastFetched`).
    case loaded(T, at: Date)
    /// The latest attempt failed. `last`/`lastAt` are the previous good value,
    /// nil when there never was one.
    case failed(FeedError, last: T?, lastAt: Date?)

    /// The loaded value, or the last good one after a failure.
    public var value: T? {
        switch self {
        case .loading: nil
        case .loaded(let v, _): v
        case .failed(_, let last, _): last
        }
    }

    /// When `value` was loaded.
    public var loadedAt: Date? {
        switch self {
        case .loading: nil
        case .loaded(_, let at): at
        case .failed(_, _, let at): at
        }
    }

    /// The error of the latest attempt, nil when it succeeded or none finished.
    public var error: FeedError? {
        if case .failed(let e, _, _) = self { return e }
        return nil
    }

    /// Failed, but still holding an older value.
    public var isStale: Bool {
        if case .failed(_, let last, _) = self { return last != nil }
        return false
    }

    public var isLoading: Bool { self == .loading }

    /// The state after a failed load: the error, keeping the last good value.
    public func afterFailure(_ error: FeedError) -> Loadable {
        .failed(error, last: value, lastAt: loadedAt)
    }

    /// Transforms the value, keeping the state.
    public func map<U: Sendable & Equatable>(_ transform: (T) -> U) -> Loadable<U> {
        switch self {
        case .loading: .loading
        case .loaded(let v, let at): .loaded(transform(v), at: at)
        case .failed(let e, let last, let at): .failed(e, last: last.map(transform), lastAt: at)
        }
    }
}
