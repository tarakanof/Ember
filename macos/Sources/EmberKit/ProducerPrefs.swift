import Foundation
import os

public protocol ProducerPrefsStoring: Sendable {
    var optOut: Set<String> { get nonmutating set }
    var knownAgents: [String]? { get nonmutating set }
}

public struct UserDefaultsProducerPrefs: ProducerPrefsStoring, @unchecked Sendable {
    private let defaults: UserDefaults
    static let optOutKey = "producers.optOut"
    static let knownAgentsKey = "producers.knownAgents"

    public init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    public var optOut: Set<String> {
        get { Set(defaults.stringArray(forKey: Self.optOutKey) ?? []) }
        nonmutating set { defaults.set(newValue.sorted(), forKey: Self.optOutKey) }
    }

    public var knownAgents: [String]? {
        get { defaults.stringArray(forKey: Self.knownAgentsKey) }
        nonmutating set { defaults.set(newValue, forKey: Self.knownAgentsKey) }
    }
}

public final class InMemoryProducerPrefs: ProducerPrefsStoring {
    private let state: OSAllocatedUnfairLock<(optOut: Set<String>, known: [String]?)>

    public init(optOut: Set<String> = [], knownAgents: [String]? = nil) {
        state = OSAllocatedUnfairLock(initialState: (optOut, knownAgents))
    }

    public var optOut: Set<String> {
        get { state.withLock { $0.optOut } }
        set { state.withLock { $0.optOut = newValue } }
    }

    public var knownAgents: [String]? {
        get { state.withLock { $0.known } }
        set { state.withLock { $0.known = newValue } }
    }
}
