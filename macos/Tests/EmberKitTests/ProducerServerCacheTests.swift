import Testing
import Foundation
@testable import EmberKit

private func write(_ json: String, under state: URL) throws {
    let dir = state.appendingPathComponent("ember")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    try Data(json.utf8).write(to: dir.appendingPathComponent("server.json"))
}

@Test func producerServerCacheReadsDefaultStateDir() throws {
    let home = FileManager.default.temporaryDirectory.appendingPathComponent("psc-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: home) }
    try write(#"{"name":"Ember","url":"http://192.0.2.36:3627"}"#, under: home.appendingPathComponent(".local/state"))
    #expect(ProducerServerCache.url(home: home, environment: [:]) == "http://192.0.2.36:3627")
}

@Test func producerServerCacheHonoursXDGStateHome() throws {
    let home = FileManager.default.temporaryDirectory.appendingPathComponent("psc-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: home) }
    try write(#"{"url":"http://192.0.2.50:3627"}"#, under: home.appendingPathComponent("xdg"))
    #expect(ProducerServerCache.url(home: home, environment: ["XDG_STATE_HOME": home.appendingPathComponent("xdg").path]) == "http://192.0.2.50:3627")
}

@Test func producerServerCacheIgnoresPreferenceMissingAndBrokenFiles() throws {
    let home = FileManager.default.temporaryDirectory.appendingPathComponent("psc-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: home) }
    #expect(ProducerServerCache.url(home: home, environment: [:]) == nil)
    let state = home.appendingPathComponent(".local/state")
    try write(#"{"url":"http://h:1","prefer":"nas"}"#, under: state)
    #expect(ProducerServerCache.url(home: home, environment: [:]) == nil)
    try write("not json", under: state)
    #expect(ProducerServerCache.url(home: home, environment: [:]) == nil)
}
