import Foundation

/// Serializes read-modify-write of producer.env.
public actor EnvFileStore {
    public nonisolated let path: URL

    public init(path: URL) { self.path = path }

    /// The file as it is now; empty when it doesn't exist yet.
    public func read() -> EnvFile {
        EnvFile(parsing: (try? String(contentsOf: path, encoding: .utf8)) ?? "")
    }

    /// Reads the file, applies `change` and writes it back, atomically with
    /// respect to other `update` calls.
    public func update(_ change: @Sendable (inout EnvFile) throws -> Void) throws {
        var env = read()
        try change(&env)
        try env.write(to: path)
    }
}
