import AppKit
import Observation
import SystemConfiguration
import EmberKit

/// Settings › Sources › Music: forwards Music.app's now playing to the
/// server while the user has it on. Event-driven: it listens for Music's
/// `com.apple.Music.playerInfo` distributed notification and does nothing
/// in between; with the toggle off it doesn't even listen.
@MainActor
@Observable
final class MusicNowPlayingWatcher {
    static let enabledKey = "musicNowPlaying.enabled"
    private static let playerInfo = Notification.Name("com.apple.Music.playerInfo")

    var enabled: Bool {
        didSet {
            guard enabled != oldValue else { return }
            UserDefaults.standard.set(enabled, forKey: Self.enabledKey)
            apply()
        }
    }

    let pusher: AppleMusicPusher
    @ObservationIgnored let bridge: AppleScriptMusicBridge
    @ObservationIgnored private var observer: NSObjectProtocol?
    @ObservationIgnored private var client: APIClient
    @ObservationIgnored private let envStore: EnvFileStore

    init(client: APIClient, envStore: EnvFileStore) {
        self.client = client
        self.envStore = envStore
        enabled = UserDefaults.standard.bool(forKey: Self.enabledKey)
        let bridge = AppleScriptMusicBridge()
        self.bridge = bridge
        pusher = AppleMusicPusher(bridge: bridge, sink: NowPlayingClient(client: client), player: Self.computerName)
    }

    func start() {
        Task {
            await configurePusher()
            apply()
        }
    }

    func reconfigure(client: APIClient) {
        self.client = client
        Task { await configurePusher() }
    }

    /// Shows macOS's Automation prompt for Music (only while Music runs).
    func requestAutomation() async -> AccessStatus? {
        await bridge.automationStatus(ask: true)
    }

    private func configurePusher() async {
        let source = ConnectionSettings(reading: await envStore.read()).source
            .trimmingCharacters(in: .whitespaces)
        pusher.configure(sink: NowPlayingClient(client: client), player: source.isEmpty ? Self.computerName : source)
    }

    private func apply() {
        let center = DistributedNotificationCenter.default()
        if enabled, observer == nil {
            observer = center.addObserver(forName: Self.playerInfo, object: nil, queue: .main) { [weak self] note in
                guard let info = MusicPlayerInfo(userInfo: note.userInfo ?? [:]) else { return }
                MainActor.assumeIsolated { self?.pusher.submit(info) }
            }
            Task { await pusher.pushSnapshot() }
        } else if !enabled, let o = observer {
            center.removeObserver(o)
            observer = nil
            Task { await pusher.stop() }
        }
    }

    private static var computerName: String {
        (SCDynamicStoreCopyComputerName(nil, nil) as String?) ?? "Mac"
    }
}
