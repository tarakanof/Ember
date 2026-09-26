import Testing
@testable import EmberKit

@Test func serverURLRules() {
    #expect((try? validateServerURL("http://192.168.0.14")) == "http://192.168.0.14")
    #expect((try? validateServerURL("https://host:3627/x")) == "https://host:3627/x")
    #expect((try? validateServerURL("")) == nil)
    #expect((try? validateServerURL("ftp://h")) == nil)
    #expect((try? validateServerURL("http://u:p@h")) == nil)
    #expect((try? validateServerURL("notaurl")) == nil)
}

@Test func sourceColorRules() {
    #expect((try? validateSourceColor("#1aFF9c")) == "#1aFF9c")
    #expect((try? validateSourceColor("")) == "")
    #expect((try? validateSourceColor("1aff9c")) == nil)
    #expect((try? validateSourceColor("#ggg")) == nil)
}

/// Validation errors reach the UI (the token footer, a save error), so they
/// are localizable sentences, not log-style fragments.
@Test func validationErrorsAreUserFacingSentences() {
    func message(_ body: () throws -> Void) -> String? {
        do { try body(); return nil } catch let e as ValidationError { return e.message.text } catch { return nil }
    }
    #expect(message { _ = try validateServerURL("") } == "Enter the server's URL.")
    #expect(message { _ = try validateServerURL("ftp://h") }
        == "The server URL must start with http:// or https://, include a host, and have no user name or password.")
    #expect(message { _ = try validateSource(" ") } == "Enter a source name.")
    #expect(message { _ = try validateSourceColor("red") } == "The color must be a hex value like #FF8800.")
    #expect(message { _ = try validateToken("a\u{01}") } == "The value can't contain control characters.")
    #expect(ValidationError(message: "Enter a source name.").errorDescription == "Enter a source name.")
}

@Test func sourceAndTokenRules() {
    #expect((try? validateSource("mbp")) == "mbp")
    #expect((try? validateSource("")) == nil)
    #expect((try? validateToken("anything")) == "anything")
    #expect((try? validateToken("")) == "")
    #expect((try? validateToken("bad\u{01}ctrl")) == nil)
}
