import Testing
import Foundation
@testable import EmberKit

@Test func fireSendsJSONToFireEndpoint() async throws {
    let client = stubbedClient(token: "t") { req in
        #expect(req.httpMethod == "POST")
        #expect(req.url?.path == "/v1/reminders/fire")
        let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let obj = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        #expect(obj?["text"] as? String == "Walk")
        #expect(obj?["sound"] as? Bool == true)
        #expect(obj?["duration"] as? Int == 8)
        #expect(obj?["native_icon_id"] as? String == "1234")
        #expect(obj?["hold"] as? Bool == true)
        #expect(obj?["repeat_sound"] as? Bool == false)
        #expect(req.value(forHTTPHeaderField: "Idempotency-Key") == "abc|1000")
        return (okResponse(req.url!, status: 204), Data())
    }
    try await RemindersService(client: client).fire(text: "Walk", sound: true, duration: 8, nativeIconId: "1234", hold: true, key: "abc|1000")
}

@Test func fireSendsRepeatSoundWhenOptedIn() async throws {
    let client = stubbedClient(token: "t") { req in
        let body = req.httpBodyStreamData() ?? req.httpBody ?? Data()
        let obj = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        #expect(obj?["repeat_sound"] as? Bool == true)
        return (okResponse(req.url!, status: 204), Data())
    }
    try await RemindersService(client: client).fire(text: "Walk", sound: true, duration: 8, nativeIconId: "",
                                                    hold: true, repeatSound: true, key: "k")
}

@Test func reminderPrefsRepeatSoundDefaultsOff() throws {
    #expect(ReminderPrefs().repeatSound == false)
    let old = try JSONDecoder().decode(ReminderPrefs.self, from: Data(#"{"enabled":true,"hold":true}"#.utf8))
    #expect(old.repeatSound == false)
}

@Test func fireReportsRefusedConnectionAsNotSent() async throws {
    let client = stubbedClient { _ in throw URLError(.cannotConnectToHost) }
    await #expect(throws: RequestNotSent.self) {
        try await RemindersService(client: client).fire(text: "Walk", sound: false, duration: 8,
                                                        nativeIconId: "", hold: false, key: "k")
    }
}

@Test func fireReportsTimeoutAsPlainTransportError() async throws {
    let client = stubbedClient { _ in throw URLError(.timedOut) }
    do {
        try await RemindersService(client: client).fire(text: "Walk", sound: false, duration: 8,
                                                        nativeIconId: "", hold: false, key: "k")
        Issue.record("expected an error")
    } catch {
        #expect(ReminderFireOutcome(error: error) == .maybeDelivered)
    }
}

// The server holds the fire request while it pushes to the clock (up to 10s);
// the default 5s/10s session would time out and misreport a delivered popup.
@Test func slowSessionOutlastsTheServersClockPush() {
    let client = APIClient(baseURL: URL(string: "http://example.invalid"), token: nil)
    #expect(client.slowSession.configuration.timeoutIntervalForRequest > 10)
    #expect(client.slowSession.configuration.timeoutIntervalForResource > 10)
}
