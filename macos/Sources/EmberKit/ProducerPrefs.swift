import Foundation
import os

/// Persists the Agents pane's per-agent choices: which detected agents the
/// user turned off (the master switch leaves them alone) and which agent
/// cases the app has already seen (so a newly added one can start opted out).
public protocol ProducerPrefsStoring: Sendable {
    /// Raw values of the agents the user turned off with their own switch.
    var optOut: Set<String> { get nonmutating set }
    /// Raw values of the agent cases known at the last launch; nil before the
    /// first launch that recorded them.
    var knownAgents: [String]? { get nonmutating set }
}

/// `ProducerPrefsStoring` in `UserDefaults`.
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

/// `ProducerPrefsStoring` in memory, for tests and previews.
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
