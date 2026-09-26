import CoreLocation
import EventKit
import EmberKit

/// `PermissionsModel`'s reads over the real frameworks: EventKit, CoreLocation,
/// the producer installer and a Local Network probe against the configured
/// server.
@MainActor
final class AppPermissionSources: PermissionSources {
    private let connection: ServerConnection
    private let producerService: ProducerInstallService
    private let reminderWatcher: ReminderWatcher
    private let locationService: LocationService

    init(connection: ServerConnection, producers: ProducerInstallService,
         reminders: ReminderWatcher, location: LocationService) {
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
        // Write-only access can't read due dates, so alarms can't work.
        default: .denied
        }
        return (status, reminderWatcher.prefs.enabled)
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
