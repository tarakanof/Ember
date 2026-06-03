import Foundation

extension APIClient {
    /// Builds a client from a producer.env: EMBER_SERVER_URL -> baseURL (nil when
    /// blank/unparseable), EMBER_TOKEN -> token (nil when blank). Non-fatal: a
    /// nil baseURL just yields APIError.notConfigured on use (app shows Offline).
    public init(producerEnv env: EnvFile) {
        let urlString = env.get(SettingsKeys.serverURL)
        let token = env.get(SettingsKeys.token)
        self.init(baseURL: urlString.isEmpty ? nil : URL(string: urlString),
                  token: token.isEmpty ? nil : token)
    }
}
