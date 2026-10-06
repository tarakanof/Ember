import AppKit
import Observation
import SystemConfiguration
import EmberKit

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
    @ObservationIgnored private let commands: MusicCommandListener
    @ObservationIgnored private var player: String = MusicNowPlayingWatcher.computerName
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
        commands = MusicCommandListener(bridge: bridge, pusher: pusher)
    }

    func start() {
        Task {
            await configurePusher()
            apply()
        }
    }

    func reconfigure(client: APIClient) {
        self.client = client
        Task {
            await configurePusher()
            if enabled { listen() }
        }
    }

    func requestAutomation() async -> AccessStatus? {
        await bridge.automationStatus(ask: true)
    }

    private func configurePusher() async {
        let source = ConnectionSettings(reading: await envStore.read()).source
            .trimmingCharacters(in: .whitespaces)
        player = source.isEmpty ? Self.computerName : source
        await pusher.configure(sink: NowPlayingClient(client: client), player: player)
    }

    private func listen() {
        commands.start(source: NowPlayingClient(client: client), player: player)
    }

    private func apply() {
        let center = DistributedNotificationCenter.default()
        if enabled, observer == nil {
            observer = center.addObserver(forName: Self.playerInfo, object: nil, queue: .main) { [weak self] note in
                guard let info = MusicPlayerInfo(userInfo: note.userInfo ?? [:]) else { return }
                MainActor.assumeIsolated { self?.pusher.submit(info) }
            }
            Task { await pusher.pushSnapshot() }
            listen()
        } else if !enabled, let o = observer {
            center.removeObserver(o)
            observer = nil
            commands.stop()
            Task { await pusher.stop() }
        }
    }

    private static var computerName: String {
        (SCDynamicStoreCopyComputerName(nil, nil) as String?) ?? "Mac"
    }
}
