import Foundation

/// The per-agent producer.env settings under Settings › Sources › Agents,
/// parsed and validated the way the Go producers read them
/// (`cmd/ember-codex-producer/config.go`, `cmd/ember-claude-producer`).
public struct ProducerTuning: Equatable, Sendable {
    /// The Codex `session_meta.source` kinds the pane offers, in display order.
    public static let codexSourceKinds = ["cli", "vscode", "exec", "mcp"]
    static let defaultCodexSources: Set<String> = ["cli", "vscode"]
    static let defaultDoneTTLSeconds = 30
    static let defaultStatuslineTimeoutMs = 10_000

    public var codexIncludeClaude: Bool
    /// Never empty when read: the producer treats an empty list as the default.
    public var codexSources: Set<String>
    public var codexAppServer: Bool
    public var doneTTLSeconds: Int
    public var claudeAgentsPoll: Bool
    public var statuslineTimeoutMs: Int

    public init(reading env: EnvFile) {
        codexIncludeClaude = envOn(env.get(SettingsKeys.codexIncludeClaude))
        codexSources = Self.parseSources(env.get(SettingsKeys.codexSources))
        codexAppServer = envTrue(env.get(SettingsKeys.codexAppServer))
        doneTTLSeconds = Self.positiveInt(env.get(SettingsKeys.doneTTLSeconds)) ?? Self.defaultDoneTTLSeconds
        claudeAgentsPoll = envTrue(env.get(SettingsKeys.claudeAgentsPoll))
        statuslineTimeoutMs = Self.positiveInt(env.get(SettingsKeys.statuslineTimeoutMs))
            ?? Self.defaultStatuslineTimeoutMs
    }

    /// Validates every value, then writes each one that differs from
    /// `previous` (the value last loaded or saved; nil compares with the
    /// file), so a hand edit to another key survives. A value equal to the
    /// producer's default removes its key. A throw writes nothing.
    public func apply(to env: inout EnvFile, from previous: ProducerTuning? = nil) throws {
        if codexSources.isEmpty {
            throw ValidationError(message: "Choose at least one kind of Codex session to show.")
        }
        if doneTTLSeconds <= 0 || statuslineTimeoutMs <= 0 {
            throw ValidationError(message: "The value must be a positive whole number.")
        }
        let base = previous ?? ProducerTuning(reading: env)
        let defaults = ProducerTuning(reading: EnvFile(parsing: ""))
        func write<V: Equatable>(_ key: String, _ path: KeyPath<ProducerTuning, V>, _ text: (V) -> String) {
            guard self[keyPath: path] != base[keyPath: path] else { return }
            if self[keyPath: path] == defaults[keyPath: path] {
                env.remove(key)
            } else {
                env.set(key, text(self[keyPath: path]))
            }
        }
        func b(_ v: Bool) -> String { v ? "true" : "false" }
        write(SettingsKeys.codexIncludeClaude, \.codexIncludeClaude, b)
        write(SettingsKeys.codexSources, \.codexSources, Self.sourceList)
        write(SettingsKeys.codexAppServer, \.codexAppServer, b)
        write(SettingsKeys.doneTTLSeconds, \.doneTTLSeconds, String.init)
        write(SettingsKeys.claudeAgentsPoll, \.claudeAgentsPoll, b)
        write(SettingsKeys.statuslineTimeoutMs, \.statuslineTimeoutMs, String.init)
    }

    /// Whether a Codex setting differs, which the Codex producer only reads
    /// when it starts.
    public func changesCodex(from other: ProducerTuning) -> Bool {
        codexIncludeClaude != other.codexIncludeClaude
            || codexSources != other.codexSources
            || codexAppServer != other.codexAppServer
    }

    /// Whether unchecking `kind` leaves at least one kind checked.
    public func canUncheckCodexSource(_ kind: String) -> Bool {
        !codexSources.subtracting([kind]).isEmpty
    }

    /// Checks or unchecks a Codex source kind; never unchecks the last one.
    public mutating func setCodexSource(_ kind: String, on: Bool) {
        if on {
            codexSources.insert(kind)
        } else if canUncheckCodexSource(kind) {
            codexSources.remove(kind)
        }
    }

    /// Values the producers take from their process environment ahead of
    /// producer.env; nil where the variable isn't set.
    public struct Overrides: Equatable, Sendable {
        public var claudeAgentsPoll: Bool?
        public var statuslineTimeoutMs: Int?

        public init() {}

        /// Reads the variables the producers look up with `os.LookupEnv`: set
        /// but empty or invalid still wins, with the producer's default.
        public init(environment: [String: String]) {
            if let v = environment[SettingsKeys.claudeAgentsPoll] {
                claudeAgentsPoll = envTrue(v)
            }
            if let v = environment[SettingsKeys.statuslineTimeoutMs] {
                statuslineTimeoutMs = ProducerTuning.positiveInt(v) ?? ProducerTuning.defaultStatuslineTimeoutMs
            }
        }
    }

    static func parseSources(_ v: String) -> Set<String> {
        let kinds = v.split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces).lowercased() }
            .filter { !$0.isEmpty }
        return kinds.isEmpty ? defaultCodexSources : Set(kinds)
    }

    static func sourceList(_ set: Set<String>) -> String {
        let known = codexSourceKinds.filter(set.contains)
        let other = set.subtracting(codexSourceKinds).sorted()
        return (known + other).joined(separator: ",")
    }

    static func positiveInt(_ v: String) -> Int? {
        guard let n = Int(v.trimmingCharacters(in: .whitespaces)), n > 0 else { return nil }
        return n
    }
}
