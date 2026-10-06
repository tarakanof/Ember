import Foundation

public enum Loadable<T: Sendable & Equatable>: Equatable, Sendable {
    case loading
    case loaded(T, at: Date)
    case failed(FeedError, last: T?, lastAt: Date?)

    public var value: T? {
        switch self {
        case .loading: nil
        case .loaded(let v, _): v
        case .failed(_, let last, _): last
        }
    }

    public var loadedAt: Date? {
        switch self {
        case .loading: nil
        case .loaded(_, let at): at
        case .failed(_, _, let at): at
        }
    }

    public var error: FeedError? {
        if case .failed(let e, _, _) = self { return e }
        return nil
    }

    public var isStale: Bool {
        if case .failed(_, let last, _) = self { return last != nil }
        return false
    }

    public var isLoading: Bool { self == .loading }

    public func afterFailure(_ error: FeedError) -> Loadable {
        .failed(error, last: value, lastAt: loadedAt)
    }

    public func map<U: Sendable & Equatable>(_ transform: (T) -> U) -> Loadable<U> {
        switch self {
        case .loading: .loading
        case .loaded(let v, let at): .loaded(transform(v), at: at)
        case .failed(let e, let last, let at): .failed(e, last: last.map(transform), lastAt: at)
        }
    }
}
