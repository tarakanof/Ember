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

public final class FileProducerPrefs: ProducerPrefsStoring, @unchecked Sendable {
    private struct Stored: Codable {
        var optOut: [String]
        var knownAgents: [String]?
    }

    private let file: URL
    private let lock = NSLock()
    private var cached: Stored

    public init(file: URL, migratingFrom legacy: ProducerPrefsStoring? = nil) {
        self.file = file
        if let data = try? Data(contentsOf: file), let stored = try? JSONDecoder().decode(Stored.self, from: data) {
            cached = stored
            return
        }
        cached = Stored(optOut: [], knownAgents: nil)
        guard !FileManager.default.fileExists(atPath: file.path), let legacy else { return }
        cached = Stored(optOut: legacy.optOut.sorted(), knownAgents: legacy.knownAgents)
        if !cached.optOut.isEmpty || cached.knownAgents != nil { persist() }
    }

    public var optOut: Set<String> {
        get { lock.withLock { Set(cached.optOut) } }
        set {
            lock.withLock {
                cached.optOut = newValue.sorted()
                persist()
            }
        }
    }

    public var knownAgents: [String]? {
        get { lock.withLock { cached.knownAgents } }
        set {
            lock.withLock {
                cached.knownAgents = newValue
                persist()
            }
        }
    }

    private func persist() {
        do { try write(cached) } catch {
            Logger(subsystem: "com.ember.Ember", category: "producers")
                .error("producers.json not saved: \(error.localizedDescription, privacy: .public)")
        }
    }

    private func write(_ stored: Stored) throws {
        let fm = FileManager.default
        let dir = file.deletingLastPathComponent()
        try fm.createDirectory(at: dir, withIntermediateDirectories: true,
                               attributes: [.posixPermissions: 0o700])
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        let tmp = dir.appendingPathComponent(".tmp-\(UUID().uuidString).json")
        try encoder.encode(stored).write(to: tmp)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tmp.path)
        if fm.fileExists(atPath: file.path) {
            _ = try fm.replaceItemAt(file, withItemAt: tmp)
        } else {
            try fm.moveItem(at: tmp, to: file)
        }
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
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
