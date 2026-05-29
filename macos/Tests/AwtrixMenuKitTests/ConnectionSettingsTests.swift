import Testing
@testable import AwtrixMenuKit

@Test func readsConnectionFromEnv() {
    let env = EnvFile(parsing: """
    STATUS_SOURCE=mbp
    STATUS_SERVER_URL=http://192.168.0.14
    STATUS_SOURCE_COLOR=#2ee85e
    STATUS_TOKEN=secret
    """)
    let c = ConnectionSettings(reading: env)
    #expect(c.source == "mbp")
    #expect(c.serverURL == "http://192.168.0.14")
    #expect(c.sourceColor == "#2ee85e")
    #expect(ConnectionSettings.tokenIsSet(in: env))
}

@Test func tokenUnsetWhenBlank() {
    let env = EnvFile(parsing: "STATUS_SOURCE=mbp\n")
    #expect(!ConnectionSettings.tokenIsSet(in: env))
}

@Test func appliesConnectionSettingValuesAndKeepsTokenWhenBlank() throws {
    var env = EnvFile(parsing: "STATUS_TOKEN=existing\n")
    let c = ConnectionSettings(source: "laptop", serverURL: "https://h:8080", sourceColor: "#ff8800")
    try c.apply(to: &env, token: "")
    #expect(env.get(SettingsKeys.source) == "laptop")
    #expect(env.get(SettingsKeys.serverURL) == "https://h:8080")
    #expect(env.get(SettingsKeys.sourceColor) == "#ff8800")
    #expect(env.get(SettingsKeys.token) == "existing")
}

@Test func appliesNewTokenWhenProvided() throws {
    var env = EnvFile(parsing: "STATUS_TOKEN=old\n")
    let c = ConnectionSettings(source: "mbp", serverURL: "http://h", sourceColor: "")
    try c.apply(to: &env, token: "  newtok  ")
    #expect(env.get(SettingsKeys.token) == "newtok")
    #expect(env.get(SettingsKeys.sourceColor) == "")
}

@Test func applyRejectsInvalidURL() {
    var env = EnvFile(parsing: "")
    let c = ConnectionSettings(source: "mbp", serverURL: "ftp://nope", sourceColor: "")
    #expect(throws: ValidationError.self) { try c.apply(to: &env, token: nil) }
}
