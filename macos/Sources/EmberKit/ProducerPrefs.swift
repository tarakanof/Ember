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

    public init(file: URL, migratingFrom legacy: ProducerPrefsStoring? = nil) {
        self.file = file
        guard !FileManager.default.fileExists(atPath: file.path), let legacy else { return }
        let optOut = legacy.optOut
        let known = legacy.knownAgents
        if !optOut.isEmpty || known != nil {
            try? write(Stored(optOut: optOut.sorted(), knownAgents: known))
        }
    }

    public var optOut: Set<String> {
        get { lock.withLock { Set(read().optOut) } }
        set {
            lock.withLock {
                var stored = read()
                stored.optOut = newValue.sorted()
                try? write(stored)
            }
        }
    }

    public var knownAgents: [String]? {
        get { lock.withLock { read().knownAgents } }
        set {
            lock.withLock {
                var stored = read()
                stored.knownAgents = newValue
                try? write(stored)
            }
        }
    }

    private func read() -> Stored {
        guard let data = try? Data(contentsOf: file),
              let stored = try? JSONDecoder().decode(Stored.self, from: data) else {
            return Stored(optOut: [], knownAgents: nil)
        }
        return stored
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
