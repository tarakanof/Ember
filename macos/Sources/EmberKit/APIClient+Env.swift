import Foundation

extension APIClient {
    /// Builds a client from a producer.env: EMBER_SERVER_URL -> baseURL (nil when
    /// blank/unparseable or "auto", the producers' mDNS discovery keyword),
    /// EMBER_TOKEN -> token (nil when blank).
    public init(producerEnv env: EnvFile) {
        let urlString = env.get(SettingsKeys.serverURL)
        let token = env.get(SettingsKeys.token)
        let noURL = urlString.isEmpty || isAutoServerURL(urlString)
        self.init(baseURL: noURL ? nil : URL(string: urlString),
                  token: token.isEmpty ? nil : token)
    }
}
