import Foundation

public actor EnvFileStore {
    public nonisolated let path: URL

    public init(path: URL) { self.path = path }

    public func read() -> EnvFile {
        EnvFile(parsing: (try? String(contentsOf: path, encoding: .utf8)) ?? "")
    }

    public func update(_ change: @Sendable (inout EnvFile) throws -> Void) throws {
        var env = read()
        try change(&env)
        try env.write(to: path)
    }
}
