import Foundation

public enum ProducerServerCache {
    public static func url(home: URL, environment: [String: String] = ProcessInfo.processInfo.environment) -> String? {
        let state: URL
        if let xdg = environment["XDG_STATE_HOME"], xdg.hasPrefix("/") {
            state = URL(fileURLWithPath: xdg)
        } else {
            state = home.appendingPathComponent(".local/state")
        }
        let file = state.appendingPathComponent("ember/server.json")
        guard let data = try? Data(contentsOf: file),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let url = object["url"] as? String, !url.isEmpty,
              ((object["prefer"] as? String) ?? "").isEmpty
        else { return nil }
        return url
    }
}
