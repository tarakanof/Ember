import Testing

@MainActor
func waitFor(_ cond: () -> Bool) async throws {
    for _ in 0..<1000 where !cond() { try await Task.sleep(for: .milliseconds(5)) }
    try #require(cond())
}
