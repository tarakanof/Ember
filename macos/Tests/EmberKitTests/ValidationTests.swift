import Testing
@testable import EmberKit

@Test func serverURLRules() {
    #expect((try? validateServerURL("http://192.168.0.14")) == "http://192.168.0.14")
    #expect((try? validateServerURL("https://host:8080/x")) == "https://host:8080/x")
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

@Test func sourceAndTokenRules() {
    #expect((try? validateSource("mbp")) == "mbp")
    #expect((try? validateSource("")) == nil)
    #expect((try? validateToken("anything")) == "anything")
    #expect((try? validateToken("")) == "")
    #expect((try? validateToken("bad\u{01}ctrl")) == nil)
}
