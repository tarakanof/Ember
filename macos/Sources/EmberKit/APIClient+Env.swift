import Foundation

extension APIClient {
    public init(producerEnv env: EnvFile) {
        let urlString = env.get(SettingsKeys.serverURL)
        let token = env.get(SettingsKeys.token)
        let noURL = urlString.isEmpty || isAutoServerURL(urlString)
        self.init(baseURL: noURL ? nil : URL(string: urlString),
                  token: token.isEmpty ? nil : token)
    }
}
