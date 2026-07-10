import Testing
@testable import EmberKit

@Test func readsConnectionFromEnv() {
    let env = EnvFile(parsing: """
    EMBER_SOURCE=mbp
    EMBER_SERVER_URL=http://192.168.0.14
    EMBER_SOURCE_COLOR=#2ee85e
    EMBER_TOKEN=secret
    """)
    let c = ConnectionSettings(reading: env)
    #expect(c.source == "mbp")
    #expect(c.serverURL == "http://192.168.0.14")
    #expect(c.sourceColor == "#2ee85e")
    #expect(ConnectionSettings.tokenIsSet(in: env))
}

@Test func tokenUnsetWhenBlank() {
    let env = EnvFile(parsing: "EMBER_SOURCE=mbp\n")
    #expect(!ConnectionSettings.tokenIsSet(in: env))
}

@Test func appliesConnectionSettingValuesAndKeepsTokenWhenBlank() throws {
    var env = EnvFile(parsing: "EMBER_TOKEN=existing\n")
    let c = ConnectionSettings(source: "laptop", serverURL: "https://h:3627", sourceColor: "#ff8800")
    try c.apply(to: &env, token: "")
    #expect(env.get(SettingsKeys.source) == "laptop")
    #expect(env.get(SettingsKeys.serverURL) == "https://h:3627")
    #expect(env.get(SettingsKeys.sourceColor) == "#ff8800")
    #expect(env.get(SettingsKeys.token) == "existing")
}

@Test func appliesNewTokenWhenProvided() throws {
    var env = EnvFile(parsing: "EMBER_TOKEN=old\n")
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

// MARK: first-run tolerant apply (fill fields in any order without deadlock)

// First run: only the Server URL is filled. A still-empty required Source must
// NOT throw (no "source must not be empty" red error), and the URL is written.
@Test func tolerantApplyWritesServerURLWithSourceStillEmpty() throws {
    var env = EnvFile(parsing: "")
    let c = ConnectionSettings(source: "", serverURL: "http://192.168.0.14:3627", sourceColor: "")
    try c.applyTolerant(to: &env, token: nil)
    #expect(env.get(SettingsKeys.serverURL) == "http://192.168.0.14:3627")
    #expect(env.get(SettingsKeys.source) == "")
}

// First run: only the Source is filled. A still-empty required Server URL must
// NOT throw, and the source is written.
@Test func tolerantApplyWritesSourceWithServerURLStillEmpty() throws {
    var env = EnvFile(parsing: "")
    let c = ConnectionSettings(source: "mbp", serverURL: "", sourceColor: "")
    try c.applyTolerant(to: &env, token: nil)
    #expect(env.get(SettingsKeys.source) == "mbp")
    #expect(env.get(SettingsKeys.serverURL) == "")
}

// Filling both fields (in any order) converges to a complete configuration.
@Test func tolerantApplyWritesBothWhenComplete() throws {
    var env = EnvFile(parsing: "")
    let step1 = ConnectionSettings(source: "", serverURL: "http://h:3627", sourceColor: "")
    try step1.applyTolerant(to: &env, token: nil)
    let step2 = ConnectionSettings(source: "mbp", serverURL: "http://h:3627", sourceColor: "")
    try step2.applyTolerant(to: &env, token: nil)
    #expect(env.get(SettingsKeys.source) == "mbp")
    #expect(env.get(SettingsKeys.serverURL) == "http://h:3627")
}

// A genuinely invalid (but non-empty) URL still throws, even while Source is
// empty — the red-error UX for bad values is preserved.
@Test func tolerantApplyStillRejectsGenuinelyBadURL() {
    var env = EnvFile(parsing: "")
    let c = ConnectionSettings(source: "", serverURL: "ftp://nope", sourceColor: "")
    #expect(throws: ValidationError.self) { try c.applyTolerant(to: &env, token: nil) }
}

// A genuinely invalid color still throws.
@Test func tolerantApplyStillRejectsBadColor() {
    var env = EnvFile(parsing: "")
    let c = ConnectionSettings(source: "mbp", serverURL: "http://h", sourceColor: "orange")
    #expect(throws: ValidationError.self) { try c.applyTolerant(to: &env, token: nil) }
}

// A token can be saved on a fresh install before Source/Server URL are filled.
@Test func tolerantApplyWritesTokenOnFirstRun() throws {
    var env = EnvFile(parsing: "")
    let c = ConnectionSettings(source: "", serverURL: "", sourceColor: "")
    try c.applyTolerant(to: &env, token: "  secret  ")
    #expect(env.get(SettingsKeys.token) == "secret")
}

// MARK: clearing an already-set required field (Finding 4)

// First run with a genuinely empty env: an empty required field is tolerated —
// no error is thrown just because nothing is configured yet.
@Test func tolerantApplyToleratesEmptyRequiredFieldOnFirstRun() throws {
    var env = EnvFile(parsing: "")
    let c = ConnectionSettings(source: "", serverURL: "", sourceColor: "")
    try c.applyTolerant(to: &env, token: nil)   // must not throw
    #expect(env.get(SettingsKeys.source) == "")
    #expect(env.get(SettingsKeys.serverURL) == "")
}

// Clearing an already-committed Source surfaces an error AND does not drop the
// stored value (producer.env keeps the old source).
@Test func tolerantApplyRejectsClearingAlreadySetSource() {
    var env = EnvFile(parsing: "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=http://h:3627\n")
    let cleared = ConnectionSettings(source: "  ", serverURL: "http://h:3627", sourceColor: "")
    #expect(throws: ValidationError.self) { try cleared.applyTolerant(to: &env, token: nil) }
    #expect(env.get(SettingsKeys.source) == "mbp")   // old value not silently dropped
}

// Clearing an already-committed Server URL surfaces an error AND keeps the value.
@Test func tolerantApplyRejectsClearingAlreadySetServerURL() {
    var env = EnvFile(parsing: "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=http://h:3627\n")
    let cleared = ConnectionSettings(source: "mbp", serverURL: "", sourceColor: "")
    #expect(throws: ValidationError.self) { try cleared.applyTolerant(to: &env, token: nil) }
    #expect(env.get(SettingsKeys.serverURL) == "http://h:3627")
}

// A valid edit of an already-configured connection still applies normally.
@Test func tolerantApplyEditsAlreadySetFields() throws {
    var env = EnvFile(parsing: "EMBER_SOURCE=mbp\nEMBER_SERVER_URL=http://h:3627\n")
    let edit = ConnectionSettings(source: "laptop", serverURL: "http://h:3627", sourceColor: "")
    try edit.applyTolerant(to: &env, token: nil)
    #expect(env.get(SettingsKeys.source) == "laptop")
    #expect(env.get(SettingsKeys.serverURL) == "http://h:3627")
}

@Test func tolerantApplyIsCompleteReflectsRequiredFields() {
    #expect(!ConnectionSettings(source: "", serverURL: "http://h", sourceColor: "").isComplete)
    #expect(!ConnectionSettings(source: "mbp", serverURL: "  ", sourceColor: "").isComplete)
    #expect(ConnectionSettings(source: "mbp", serverURL: "http://h", sourceColor: "").isComplete)
}
