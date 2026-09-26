import Foundation

/// Serializes read-modify-write of producer.env. Several settings models (and
/// the Connection pane's token save) write the same file; without one queue
/// two concurrent saves read the same old file and the second write drops the
/// first's change. Every in-app writer goes through one store per path.
public actor EnvFileStore {
    public nonisolated let path: URL

    public init(path: URL) { self.path = path }

    /// The file as it is now; empty when it doesn't exist yet.
    public func read() -> EnvFile {
        EnvFile(parsing: (try? String(contentsOf: path, encoding: .utf8)) ?? "")
    }

    /// Reads the file, applies `change` and writes it back, atomically with
    /// respect to other `update` calls. A throw from `change` writes nothing.
    public func update(_ change: @Sendable (inout EnvFile) throws -> Void) throws {
        var env = read()
        try change(&env)
        try env.write(to: path)
    }
}
