import Foundation
import AwtrixMenuKit

/// Reads ~/.config/awtrix-ai-status/producer.env and builds an APIClient from
/// STATUS_SERVER_URL / STATUS_TOKEN. Missing file or keys -> a client with a nil
/// baseURL, so AppModel.refresh() simply reports disconnected (non-fatal).
enum Bootstrap {
    static var producerEnvPath: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/awtrix-ai-status/producer.env")
    }

    static func makeClient() -> APIClient {
        let text = (try? String(contentsOf: producerEnvPath, encoding: .utf8)) ?? ""
        let env = EnvFile(parsing: text)
        let urlString = env.get("STATUS_SERVER_URL")
        let token = env.get("STATUS_TOKEN")
        let baseURL = urlString.isEmpty ? nil : URL(string: urlString)
        return APIClient(baseURL: baseURL, token: token.isEmpty ? nil : token)
    }
}
