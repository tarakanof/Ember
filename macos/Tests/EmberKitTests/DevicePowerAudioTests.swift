import Testing
import Foundation
@testable import EmberKit

private func bodyObject(_ req: URLRequest) throws -> [String: Any] {
    let data = req.httpBodyStreamData() ?? req.httpBody ?? Data()
    return try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
}

@Test func setDisplayPowerSendsPowerOnly() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "PUT")
        #expect(req.url?.path == "/v1/device/display/power")
        let obj = try bodyObject(req)
        #expect(obj["power"] as? Bool == false)
        #expect(obj.count == 1)
        return (okResponse(req.url!), Data())
    }
    try await DeviceService(client: client).setDisplayPower(false)
}

@Test func playTestChimeWithoutMelodySendsNoBody() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "POST")
        #expect(req.url?.path == "/v1/device/audio/test")
        #expect((req.httpBodyStreamData() ?? req.httpBody ?? Data()).isEmpty)
        return (okResponse(req.url!), Data())
    }
    try await DeviceService(client: client).playTestChime()
}

@Test func playTestChimeWithMelodySendsName() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "POST")
        #expect(req.url?.path == "/v1/device/audio/test")
        let obj = try bodyObject(req)
        #expect(obj["melody"] as? String == "doorbell")
        return (okResponse(req.url!), Data())
    }
    try await DeviceService(client: client).playTestChime(melody: "doorbell")
}

@Test func stopAudioPosts() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "POST")
        #expect(req.url?.path == "/v1/device/audio/stop")
        return (okResponse(req.url!), Data())
    }
    try await DeviceService(client: client).stopAudio()
}

@Test func melodiesDecodesNGList() async throws {
    // Shape from device_audio_test.go / NG's GET /api/v1/audio/melodies.
    let json = #"""
    {"melodies":[
      {"name":"doorbell","rtttl":"doorbell:d=4,o=5,b=100:e,c","bytes":26,"notes":2,"durationMs":2400,"valid":true},
      {"name":"broken","rtttl":"broken:x","bytes":8,"notes":0,"durationMs":0,"valid":false,"error":"bad note","index":7}],
     "usedBytes":41216,"totalBytes":1048576}
    """#
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "GET")
        #expect(req.url?.path == "/v1/device/audio/melodies")
        return (okResponse(req.url!), Data(json.utf8))
    }
    let list = try await DeviceService(client: client).melodies()
    #expect(list.usedBytes == 41216)
    #expect(list.totalBytes == 1048576)
    #expect(list.melodies.map(\.name) == ["doorbell", "broken"])
    let d = list.melodies[0]
    #expect(d.id == "doorbell")
    #expect(d.rtttl == "doorbell:d=4,o=5,b=100:e,c")
    #expect(d.notes == 2 && d.durationMs == 2400 && d.bytes == 26)
    #expect(d.valid && d.error == nil && d.index == nil)
    let b = list.melodies[1]
    #expect(!b.valid && b.error == "bad note" && b.index == 7)
}

@Test func melodiesUnavailableSurfacesAs503() async throws {
    let client = stubbedClient(token: "t") { req in
        (okResponse(req.url!, status: 503), Data(#"{"error":"clock has no buzzer","code":"unavailable"}"#.utf8))
    }
    await #expect(throws: APIError.self) {
        _ = try await DeviceService(client: client).melodies()
    }
}

@Test func deviceDisplayDecodesPowerButNeverSendsIt() throws {
    let json = #"{"power":false,"overlay":"rain"}"#
    let d = try JSONDecoder().decode(DeviceDisplay.self, from: Data(json.utf8))
    #expect(d.power == false)
    #expect(d.overlay == "rain")
    // PUT /v1/device/display rejects power; it has its own route.
    let obj = try JSONSerialization.jsonObject(with: JSONEncoder().encode(d)) as! [String: Any]
    #expect(obj.index(forKey: "power") == nil)
}
