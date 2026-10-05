import CoreLocation
import EventKit
import EmberKit

@MainActor
final class AppPermissionSources: PermissionSources {
    private let connection: ServerConnection
    private let producerService: ProducerInstallService
    private let reminderWatcher: ReminderWatcher
    private let locationService: LocationService
    private let musicWatcher: MusicNowPlayingWatcher

    init(connection: ServerConnection, producers: ProducerInstallService,
         reminders: ReminderWatcher, location: LocationService, music: MusicNowPlayingWatcher) {
        musicWatcher = music
        self.connection = connection
        producerService = producers
        reminderWatcher = reminders
        locationService = location
    }

    func localNetwork() async -> LocalNetworkStatus {
        await LocalNetworkProbe.run(client: connection.client)
    }

    func producers() async -> ProducerSnapshot? {
        await producerService.snapshot()
    }

    func reminders() -> (status: AccessStatus, inUse: Bool) {
        reminderWatcher.refreshAuthorization()
        let status: AccessStatus = switch reminderWatcher.authStatus {
        case .fullAccess: .granted
        case .notDetermined: .notDetermined
        default: .denied
        }
        return (status, reminderWatcher.prefs.enabled)
    }

    func musicAutomation() async -> (status: AccessStatus?, inUse: Bool) {
        guard musicWatcher.enabled else { return (nil, false) }
        return (await musicWatcher.bridge.automationStatus(ask: false), true)
    }

    func location() -> AccessStatus {
        locationService.refreshAuthorization()
        return switch locationService.authStatus {
        case .authorizedAlways, .authorizedWhenInUse: .granted
        case .notDetermined: .notDetermined
        default: .denied
        }
    }
}
