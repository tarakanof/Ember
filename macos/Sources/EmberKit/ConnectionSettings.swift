import Foundation

/// The Connection tab's editable view of producer.env (token is write-only and
/// handled separately; blank means "keep current"). Ports the retired Go menu's
/// form.go connection half of settingsForm + applyForm.
public struct ConnectionSettings: Equatable, Sendable {
    public var source: String
    public var serverURL: String
    public var sourceColor: String

    public init(source: String, serverURL: String, sourceColor: String) {
        self.source = source; self.serverURL = serverURL; self.sourceColor = sourceColor
    }

    /// Reads the current values (token loads blank — never round-trips into the UI).
    public init(reading env: EnvFile) {
        self.init(source: env.get(SettingsKeys.source),
                  serverURL: env.get(SettingsKeys.serverURL),
                  sourceColor: env.get(SettingsKeys.sourceColor))
    }

    /// Whether a token is currently stored (drives a "set"/"unset" placeholder).
    public static func tokenIsSet(in env: EnvFile) -> Bool {
        !env.get(SettingsKeys.token).isEmpty
    }

    /// Validates ALL fields, then writes them into env (so nothing is written on a
    /// validation throw). A non-blank `token` is validated + written; blank/nil
    /// leaves the existing value.
    public func apply(to env: inout EnvFile, token: String?) throws {
        let normSource = try validateSource(source)
        let normURL = try validateServerURL(serverURL)
        let normColor = try validateSourceColor(sourceColor)
        var normToken: String? = nil
        if let token, !token.trimmingCharacters(in: .whitespaces).isEmpty {
            normToken = try validateToken(token)
        }
        env.set(SettingsKeys.source, normSource)
        env.set(SettingsKeys.serverURL, normURL)
        env.set(SettingsKeys.sourceColor, normColor)
        if let normToken { env.set(SettingsKeys.token, normToken) }
    }
}
