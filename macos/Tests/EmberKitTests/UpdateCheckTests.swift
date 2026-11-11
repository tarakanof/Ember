import Testing
import Foundation
@testable import EmberKit

private func release(tag: String, assets: [String] = ["Ember-1.zip"], prerelease: Bool = false) -> Data {
    let list = assets.map {
        #"{"name":"\#($0)","browser_download_url":"https://github.com/tarakanof/ember/releases/download/\#(tag)/\#($0)"}"#
    }.joined(separator: ",")
    return Data(#"{"tag_name":"\#(tag)","html_url":"https://github.com/tarakanof/ember/releases/tag/\#(tag)","prerelease":\#(prerelease),"draft":false,"assets":[\#(list)]}"#.utf8)
}

private final class FakeFetcher: UpdateFetching, @unchecked Sendable {
    private let lock = NSLock()
    private var _calls = 0
    var result: Result<Data, Error>
    var calls: Int { lock.withLock { _calls } }
    init(_ result: Result<Data, Error>) { self.result = result }
    func latestRelease() async throws -> Data {
        lock.withLock { _calls += 1 }
        return try result.get()
    }
}

private final class Clock: @unchecked Sendable {
    private let lock = NSLock()
    private var t = Date(timeIntervalSince1970: 1_800_000_000)
    var date: Date { lock.withLock { t } }
    func advance(_ s: TimeInterval) { lock.withLock { t = t.addingTimeInterval(s) } }
}

@MainActor
private func model(_ fetcher: FakeFetcher, current: String = "0.90.0", clock: Clock = Clock(),
                   defaults: UserDefaults = UserDefaults(suiteName: "update-check-\(UUID().uuidString)")!) -> UpdateCheckModel {
    return UpdateCheckModel(fetcher: fetcher, defaults: defaults, currentVersion: current, now: { clock.date })
}

@Suite struct UpdateCheckTests {
    @Test func olderTagGivesNoNotice() {
        #expect(UpdateCheck.notice(from: release(tag: "v0.89.0"), current: "0.90.0") == nil)
    }

    @Test func equalTagGivesNoNotice() {
        #expect(UpdateCheck.notice(from: release(tag: "v0.90.0"), current: "0.90.0") == nil)
    }

    @Test func newerTagGivesNotice() {
        let n = UpdateCheck.notice(from: release(tag: "v0.91.0"), current: "0.90.0")
        #expect(n?.title == "v0.91.0")
        #expect(n?.downloadURL.lastPathComponent == "Ember-1.zip")
        #expect(n?.releaseURL.lastPathComponent == "v0.91.0")
    }

    @Test func comparesNumericallyNotLexically() {
        #expect(UpdateCheck.notice(from: release(tag: "v0.100.0"), current: "0.99.0") != nil)
        #expect(UpdateCheck.notice(from: release(tag: "v0.9.0"), current: "0.10.0") == nil)
    }

    @Test func preReleaseTagIsIgnored() {
        #expect(UpdateCheck.notice(from: release(tag: "v0.91.0-rc1"), current: "0.90.0") == nil)
        #expect(UpdateCheck.notice(from: release(tag: "v0.91.0", prerelease: true), current: "0.90.0") == nil)
    }

    @Test func releaseWithoutAppZipIsIgnored() {
        #expect(UpdateCheck.notice(from: release(tag: "v0.91.0", assets: ["SHA256SUMS", "ember-producers.tar.gz"]),
                                   current: "0.90.0") == nil)
        #expect(UpdateCheck.notice(from: release(tag: "v0.91.0", assets: []), current: "0.90.0") == nil)
    }

    @Test func badJSONGivesNoNotice() {
        #expect(UpdateCheck.notice(from: Data("nope".utf8), current: "0.90.0") == nil)
    }

    @Test func nonHTTPSAssetIsIgnored() {
        let data = Data(#"{"tag_name":"v0.91.0","html_url":"https://x.test/r","assets":[{"name":"Ember-1.zip","browser_download_url":"file:///etc/passwd"}]}"#.utf8)
        #expect(UpdateCheck.notice(from: data, current: "0.90.0") == nil)
    }

    @MainActor @Test func disabledMakesNoRequest() async {
        let fetcher = FakeFetcher(.success(release(tag: "v0.91.0")))
        let m = model(fetcher)
        m.isEnabled = false
        await m.checkIfDue()
        #expect(fetcher.calls == 0)
        #expect(m.notice == nil)
    }

    @MainActor @Test func enabledByDefault() {
        #expect(model(FakeFetcher(.failure(URLError(.notConnectedToInternet)))).isEnabled)
    }

    @MainActor @Test func oneRequestPerDay() async {
        let clock = Clock()
        let fetcher = FakeFetcher(.success(release(tag: "v0.91.0")))
        let m = model(fetcher, clock: clock)
        await m.checkIfDue()
        await m.checkIfDue()
        clock.advance(23 * 3600)
        await m.checkIfDue()
        #expect(fetcher.calls == 1)
        #expect(m.notice?.title == "v0.91.0")
        clock.advance(2 * 3600)
        await m.checkIfDue()
        #expect(fetcher.calls == 2)
    }

    @MainActor @Test func failureIsSilentAndRetriedNextDay() async {
        let clock = Clock()
        let fetcher = FakeFetcher(.failure(URLError(.notConnectedToInternet)))
        let m = model(fetcher, clock: clock)
        await m.checkIfDue()
        #expect(m.notice == nil)
        await m.checkIfDue()
        #expect(fetcher.calls == 1)
        clock.advance(25 * 3600)
        fetcher.result = .success(release(tag: "v0.91.0"))
        await m.checkIfDue()
        #expect(m.notice != nil)
    }

    @MainActor @Test func turningOffClearsTheNotice() async {
        let m = model(FakeFetcher(.success(release(tag: "v0.91.0"))))
        await m.checkIfDue()
        #expect(m.notice != nil)
        m.isEnabled = false
        #expect(m.notice == nil)
    }

    @MainActor @Test func noticeSurvivesARelaunchWithoutAnotherRequest() async {
        let defaults = UserDefaults(suiteName: "update-check-\(UUID().uuidString)")!
        let clock = Clock()
        let first = model(FakeFetcher(.success(release(tag: "v0.91.0"))), clock: clock, defaults: defaults)
        await first.checkIfDue()
        let fetcher = FakeFetcher(.failure(URLError(.badURL)))
        let second = model(fetcher, clock: clock, defaults: defaults)
        #expect(second.notice?.title == "v0.91.0")
        await second.checkIfDue()
        #expect(fetcher.calls == 0)
    }

    @MainActor @Test func storedNoticeClearsOnceTheAppIsUpdated() async {
        let defaults = UserDefaults(suiteName: "update-check-\(UUID().uuidString)")!
        let first = model(FakeFetcher(.success(release(tag: "v0.91.0"))), defaults: defaults)
        await first.checkIfDue()
        #expect(model(FakeFetcher(.failure(URLError(.badURL))), current: "0.91.0", defaults: defaults).notice == nil)
    }

    @MainActor @Test func turningBackOnRestoresTheNotice() async {
        let m = model(FakeFetcher(.success(release(tag: "v0.91.0"))))
        await m.checkIfDue()
        m.isEnabled = false
        m.isEnabled = true
        #expect(m.notice?.title == "v0.91.0")
    }
}
