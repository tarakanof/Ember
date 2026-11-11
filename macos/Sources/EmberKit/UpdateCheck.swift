import Foundation
import Observation

public struct AppVersion: Sendable, Equatable, Comparable {
    public let parts: [Int]

    public init?(tag: String) {
        var s = tag.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("v") || s.hasPrefix("V") { s.removeFirst() }
        guard !s.isEmpty, !s.contains("-"), !s.contains("+") else { return nil }
        let nums = s.split(separator: ".", omittingEmptySubsequences: false).map { Int($0) }
        guard nums.count >= 1, nums.count <= 4, nums.allSatisfy({ $0 != nil && $0! >= 0 }) else { return nil }
        parts = nums.map { $0! }
    }

    public static func < (a: AppVersion, b: AppVersion) -> Bool {
        let n = max(a.parts.count, b.parts.count)
        for i in 0..<n {
            let x = i < a.parts.count ? a.parts[i] : 0
            let y = i < b.parts.count ? b.parts[i] : 0
            if x != y { return x < y }
        }
        return false
    }

    public static func == (a: AppVersion, b: AppVersion) -> Bool { !(a < b) && !(b < a) }
}

public struct UpdateNotice: Sendable, Equatable {
    public let tag: String
    public let releaseURL: URL
    public let downloadURL: URL

    public var title: String { tag.hasPrefix("v") ? tag : "v\(tag)" }
}

public enum UpdateCheck {
    public static let endpoint = URL(string: "https://api.github.com/repos/tarakanof/ember/releases/latest")!
    public static let interval: TimeInterval = 24 * 60 * 60

    private struct Release: Decodable {
        struct Asset: Decodable {
            let name: String
            let browserDownloadURL: URL
            enum CodingKeys: String, CodingKey { case name; case browserDownloadURL = "browser_download_url" }
        }
        let tagName: String
        let htmlURL: URL
        let draft: Bool?
        let prerelease: Bool?
        let assets: [Asset]?
        enum CodingKeys: String, CodingKey {
            case tagName = "tag_name"
            case htmlURL = "html_url"
            case draft, prerelease, assets
        }
    }

    public static func notice(from data: Data, current: String) -> UpdateNotice? {
        guard let release = try? JSONDecoder().decode(Release.self, from: data),
              release.draft != true, release.prerelease != true,
              let latest = AppVersion(tag: release.tagName),
              let installed = AppVersion(tag: current), installed < latest,
              let asset = release.assets?.first(where: { isAppZip($0.name) }),
              isSecure(asset.browserDownloadURL), isSecure(release.htmlURL)
        else { return nil }
        return UpdateNotice(tag: release.tagName, releaseURL: release.htmlURL, downloadURL: asset.browserDownloadURL)
    }

    static func isAppZip(_ name: String) -> Bool { name.hasPrefix("Ember-") && name.hasSuffix(".zip") }

    private static func isSecure(_ url: URL) -> Bool { url.scheme == "https" }
}

public protocol UpdateFetching: Sendable {
    func latestRelease() async throws -> Data
}

public struct GitHubUpdateFetcher: UpdateFetching {
    public init() {}

    public func latestRelease() async throws -> Data {
        var request = URLRequest(url: UpdateCheck.endpoint, timeoutInterval: 15)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("Ember", forHTTPHeaderField: "User-Agent")
        let config = URLSessionConfiguration.ephemeral
        config.httpCookieStorage = nil
        let (data, response) = try await URLSession(configuration: config).data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        return data
    }
}

@MainActor
@Observable
public final class UpdateCheckModel {
    public static let enabledKey = "updates.checkAutomatically"
    public static let lastAttemptKey = "updates.lastAttempt"

    public private(set) var notice: UpdateNotice?

    @ObservationIgnored private let fetcher: UpdateFetching
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private let currentVersion: String
    @ObservationIgnored private let now: @Sendable () -> Date
    @ObservationIgnored private var inflight = false

    public var isEnabled: Bool {
        didSet {
            defaults.set(isEnabled, forKey: Self.enabledKey)
            if !isEnabled { notice = nil }
        }
    }

    public init(fetcher: UpdateFetching, defaults: UserDefaults, currentVersion: String,
                now: @escaping @Sendable () -> Date = { Date() }) {
        self.fetcher = fetcher
        self.defaults = defaults
        isEnabled = defaults.object(forKey: Self.enabledKey) as? Bool ?? true
        self.currentVersion = currentVersion
        self.now = now
    }

    public func checkIfDue() async {
        guard isEnabled, !inflight else { return }
        let current = now()
        if let last = defaults.object(forKey: Self.lastAttemptKey) as? Date,
           last <= current, current.timeIntervalSince(last) < UpdateCheck.interval { return }
        inflight = true
        defaults.set(current, forKey: Self.lastAttemptKey)
        defer { inflight = false }
        guard let data = try? await fetcher.latestRelease() else { return }
        guard isEnabled else { return }
        notice = UpdateCheck.notice(from: data, current: currentVersion)
    }
}
