import Testing
import Foundation
@testable import EmberKit

@Test func actionPostsToCorrectPath() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "POST")
        #expect(req.url?.path == "/v1/pomodoro/start")
        return (okResponse(req.url!), Data())
    }
    let svc = PomodoroService(client: client)
    try await svc.action(.start)
}

@Test func allActionsHitTheirPaths() async throws {
    let seen = LockedBox()
    let client = stubbedClient(token: "t") { req in
        seen.add(req.url!.path)
        return (okResponse(req.url!), Data())
    }
    let svc = PomodoroService(client: client)
    for a in PomodoroAction.allCases { try await svc.action(a) }
    #expect(seen.paths.sorted() == [
        "/v1/pomodoro/pause", "/v1/pomodoro/resume", "/v1/pomodoro/skip",
        "/v1/pomodoro/start", "/v1/pomodoro/stop",
    ])
}

@Test func putConfigSendsJSON() async throws {
    let cfg = PomoConfig(focusMinutes: 25, shortBreakMinutes: 5, longBreakMinutes: 15,
                         roundsBeforeLongBreak: 4, autoStartNext: false, sound: true,
                         soundMelody: "beep", focusColor: "#ff0000", breakColor: "#00ff00")
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "PUT")
        #expect(req.url?.path == "/v1/pomodoro/config")
        let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let decoded = try JSONDecoder().decode(PomoConfig.self, from: body)
        #expect(decoded.focusMinutes == 25)
        return (okResponse(req.url!), Data())
    }
    let svc = PomodoroService(client: client)
    try await svc.putConfig(cfg)
}

final class LockedBox: @unchecked Sendable {
    private let lock = NSLock()
    private var _paths: [String] = []
    func add(_ p: String) { lock.lock(); _paths.append(p); lock.unlock() }
    var paths: [String] { lock.lock(); defer { lock.unlock() }; return _paths }
}

extension URLRequest {
    /// URLProtocol receives httpBody for non-stream bodies; this helper keeps the
    /// test resilient if a body is delivered as a stream.
    func httpBodyStreamData() -> Data? {
        guard let stream = httpBodyStream else { return nil }
        stream.open(); defer { stream.close() }
        var data = Data(); var buf = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let n = stream.read(&buf, maxLength: buf.count)
            if n <= 0 { break }
            data.append(buf, count: n)
        }
        return data.isEmpty ? nil : data
    }
}
